//go:build darwin && native

package main

/*
#cgo CFLAGS: -fobjc-arc -mmacosx-version-min=12.0
#cgo LDFLAGS: -framework Cocoa -framework WebKit -mmacosx-version-min=12.0
#include <stdlib.h>
#include "cocoawin.h"
*/
import "C"

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/httpapi"
	"github.com/didou07/dcc-lite/src/logx"
	"github.com/didou07/dcc-lite/src/vlc"
)

const nativeBuild = true

var nativeLog = logx.For("native")

func init() {
	// Cocoa and its main loop must stay on the program's first thread.
	runtime.LockOSThread()
}

var (
	nativeMu    sync.Mutex
	nativeLive_ *nativeLive
	nativeUp    atomic.Bool // the Cocoa loop is running
	nativeStop  = make(chan struct{})
	nativeOnce  sync.Once
)

// nativeClose asks the window to close. It is safe to call from any goroutine.
func nativeClose() {
	if nativeUp.Load() {
		C.dcc_cocoa_quit()
	}
	nativeOnce.Do(func() { close(nativeStop) })
}

// nativeNotice tells the user something when the program has no console (best effort).
func nativeNotice(text string) {
	nativeLog.Info(text)
	if p, err := exec.LookPath("osascript"); err == nil {
		_ = exec.Command(p, "-e", "on run argv", "-e", `display notification (item 1 of argv) with title "DCC Lite"`, "-e", "end run", text).Run()
	}
}

func browserFallback(d nativeDeps) error {
	openBrowser(d.URL)
	nativeNotice("DCC Lite is running at " + d.URL + " (stop it with Ctrl+C in its terminal).")
	<-nativeStop
	return nil
}

// runNative shows the window and returns when it is closed.
func runNative(d nativeDeps) error {
	origin := strings.TrimRight(d.URL, "/")
	live := &nativeLive{app: d.App, origin: origin, snap: stateOut{State: "opening", AudioCurrent: -1}}
	nativeMu.Lock()
	nativeLive_ = live
	nativeMu.Unlock()
	live.uiAlive.Store(time.Now().UnixNano())
	go func() { _, _ = live.vlc() }() // load VLC in the background so the first channel starts fast
	go live.poll()
	go live.watchdog()

	cURL, cOrigin, cData, cTitle := C.CString(d.URL), C.CString(origin), C.CString(d.DataDir), C.CString("DCC Lite")
	defer func() {
		for _, p := range []*C.char{cURL, cOrigin, cData, cTitle} {
			C.free(unsafe.Pointer(p))
		}
	}()
	nativeUp.Store(true)
	rc := C.dcc_cocoa_run(cURL, cOrigin, cData, cTitle, 1280, 820)
	nativeUp.Store(false)
	live.close()
	switch rc {
	case 0:
		return nil
	default:
		nativeLog.Warn("the web view could not be created; opening the interface in the default browser", "url", d.URL)
	}
	return browserFallback(d)
}

func currentLive() *nativeLive {
	nativeMu.Lock()
	defer nativeMu.Unlock()
	return nativeLive_
}

//export dccGoHandle
func dccGoHandle(op, raw *C.char, errOut **C.char) *C.char {
	n := currentLive()
	if n == nil {
		*errOut = C.CString("the player is not ready")
		return nil
	}
	out, err := n.handle(C.GoString(op), C.GoString(raw))
	if err != nil {
		*errOut = C.CString(err.Error())
		return nil
	}
	return C.CString(out)
}

//export dccGoMouse
func dccGoMouse(kind *C.char, x, y C.int) {
	if n := currentLive(); n != nil {
		n.pushMouse(C.GoString(kind), float64(x), float64(y))
	}
}

//export dccGoAlive
func dccGoAlive() {
	if n := currentLive(); n != nil {
		n.uiAlive.Store(time.Now().UnixNano())
	}
}

//export dccGoLog
func dccGoLog(level, text *C.char) {
	if C.GoString(level) == "warn" {
		nativeLog.Warn(C.GoString(text))
	} else {
		nativeLog.Info(C.GoString(text))
	}
}

// ---- Live TV through the user's VLC ----

type boxArgs struct {
	X       float64 `json:"x"`
	Y       float64 `json:"y"`
	W       float64 `json:"w"`
	H       float64 `json:"h"`
	VW      float64 `json:"vw"`
	VH      float64 `json:"vh"`
	Visible bool    `json:"visible"`
}

type playArgs struct {
	Profile   string `json:"profile"`
	Ref       string `json:"ref"`
	Name      string `json:"name"`
	Volume    int    `json:"volume"`
	Muted     bool   `json:"muted"`
	AudioLang string `json:"audioLang"`
	// Player settings, all from the one settings menu of the page.
	Deinterlace string `json:"deinterlace"` // auto, yadif or off
	Caching     int    `json:"caching"`     // buffering in milliseconds
	Hardware    *bool  `json:"hw"`          // hardware decoding; nil or true = on
	Fit         string `json:"fit"`         // fit, fill, stretch, 16:9 or 4:3
	boxArgs
}

type trackOut struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type stateOut struct {
	State        string     `json:"state"`
	Error        string     `json:"error,omitempty"`
	Width        int        `json:"width,omitempty"`
	Height       int        `json:"height,omitempty"`
	Audio        []trackOut `json:"audio"`
	AudioCurrent int        `json:"audioCurrent"`
}

// placed is where the picture window was last put, in the page window's pixels.
type placed struct {
	x, y, w, h int
	show       bool
}

type mouseEvent struct {
	Kind string  `json:"k"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

type nativeLive struct {
	app    *app.App
	origin string

	vlcOnce sync.Once
	vlcDone atomic.Bool // set when loading VLC has finished, successfully or not
	lib     *vlc.Lib
	player  *vlc.Player
	vlcErr  error

	playMu sync.Mutex // one start of the player at a time

	mu        sync.Mutex
	last      placed
	gen       int
	started   bool
	errMsg    string
	startedAt time.Time
	volume    int
	muted     bool
	audioLang string
	// audioApplied: the preferred language was applied for this channel
	audioApplied bool
	opts         vlc.PlayOptions // the player settings of the running channel
	snap         stateOut
	fit          string      // how the picture fills its window (vlc.Fit...)
	fitTimer     *time.Timer // debounces a re-fit after the box changed size
	lastVW       float64     // the page's viewport size at the last box, to turn pixels back into CSS pixels
	lastVH       float64
	events       []mouseEvent // mouse events for the page, newest last
	evMu         sync.Mutex
	evLogs       int
	fsFlag       atomic.Bool
	closed       atomic.Bool
	uiAlive      atomic.Int64
	pageAlive    atomic.Int64 // unix nanos: last time the page called in
	lastOp       atomic.Value // the request being handled, for the watchdog
}

const installHint = "VLC was not found. Install VLC (videolan.org, the VLC.app in the Applications folder), or set DCC_LIBVLC to the path of libvlc.dylib, then restart DCC Lite."

func (n *nativeLive) vlc() (*vlc.Player, error) {
	n.vlcOnce.Do(func() {
		defer n.vlcDone.Store(true)
		lib, err := vlc.Load()
		if err != nil {
			n.vlcErr = fmt.Errorf("%s (%v)", installHint, err)
			nativeLog.Warn("VLC is not available", "error", err)
			return
		}
		p, err := lib.NewPlayer()
		if err != nil {
			lib.Close()
			n.vlcErr = err
			return
		}
		n.lib, n.player = lib, p
		nativeLog.Info("VLC loaded", "library", lib.Dir())
	})
	return n.player, n.vlcErr
}

// close is called when the window has gone; the process is about to end, so nothing here may block.
func (n *nativeLive) close() {
	n.closed.Store(true)
	if n.vlcDone.Load() && n.player != nil {
		p := n.player
		go p.Stop()
	}
}

// withPlayer runs f on its own goroutine once VLC is ready. It returns at once.
func (n *nativeLive) withPlayer(f func(p *vlc.Player)) {
	if !n.vlcDone.Load() || n.player == nil {
		return
	}
	p := n.player
	go f(p)
}

// xid waits briefly for the picture window to exist (it is made while the window opens).
func (n *nativeLive) xid() (uintptr, error) {
	for i := 0; i < 100; i++ {
		if x := uintptr(C.dcc_cocoa_video_view()); x != 0 {
			return x, nil
		}
		time.Sleep(20 * time.Millisecond)
	}
	return 0, errors.New("the picture view could not be created")
}

func viewSize() (float64, float64) {
	var w, h C.int
	C.dcc_cocoa_view_size(&w, &h)
	return float64(w), float64(h)
}

func (n *nativeLive) applyBox(b boxArgs, show bool) {
	vw, vh := viewSize()
	sx, sy := 1.0, 1.0
	if b.VW > 0 && b.VH > 0 && vw > 0 && vh > 0 {
		sx, sy = vw/b.VW, vh/b.VH
	}
	p := placed{
		x: int(math.Round(b.X * sx)), y: int(math.Round(b.Y * sy)),
		w: int(math.Round(b.W * sx)), h: int(math.Round(b.H * sy)),
		show: show && b.W >= 2 && b.H >= 2,
	}
	n.mu.Lock()
	old := n.last
	n.last = p
	if b.VW > 0 && b.VH > 0 {
		n.lastVW, n.lastVH = b.VW, b.VH
	}
	n.mu.Unlock()
	if p != old || p.show {
		show := C.int(0)
		if p.show {
			show = 1
		}
		C.dcc_cocoa_box(C.int(p.x), C.int(p.y), C.int(p.w), C.int(p.h), show)
	}
	if p.w != old.w || p.h != old.h {
		n.refit()
	}
}

func (n *nativeLive) hideChild() {
	n.mu.Lock()
	p := n.last
	p.show = false
	n.last = p
	n.mu.Unlock()
	C.dcc_cocoa_box(C.int(p.x), C.int(p.y), C.int(p.w), C.int(p.h), 0)
}

func validFit(mode string) string {
	switch mode {
	case vlc.FitFill, vlc.FitStretch, vlc.Fit169, vlc.Fit43:
		return mode
	}
	return vlc.FitOriginal
}

func (n *nativeLive) applyFit() {
	n.mu.Lock()
	mode, w, h := n.fit, n.last.w, n.last.h
	n.mu.Unlock()
	n.withPlayer(func(p *vlc.Player) { p.SetFit(mode, w, h) })
}

func (n *nativeLive) refit() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fitTimer != nil {
		n.fitTimer.Stop()
	}
	n.fitTimer = time.AfterFunc(150*time.Millisecond, n.applyFit)
}

func (n *nativeLive) setFullscreen(on bool) {
	n.fsFlag.Store(on)
	v := C.int(0)
	if on {
		v = 1
	}
	C.dcc_cocoa_fullscreen(v)
	if !on {
		C.dcc_cocoa_cursor(0)
	}
}

func (n *nativeLive) setCursorHidden(hide bool) {
	v := C.int(0)
	if hide {
		v = 1
	}
	C.dcc_cocoa_cursor(v)
}

func (n *nativeLive) pushMouse(kind string, px, py float64) {
	n.mu.Lock()
	vw, vh := n.lastVW, n.lastVH
	n.mu.Unlock()
	x, y := px, py
	if w, h := viewSize(); w > 0 && h > 0 && vw > 0 && vh > 0 {
		x, y = px*vw/w, py*vh/h
	}
	n.evMu.Lock()
	defer n.evMu.Unlock()
	if kind == "move" { // only the newest position matters
		for i := range n.events {
			if n.events[i].Kind == "move" {
				n.events = append(n.events[:i], n.events[i+1:]...)
				break
			}
		}
	} else if n.evLogs < 6 {
		n.evLogs++
		nativeLog.Info("mouse over the picture", "event", kind)
	}
	n.events = append(n.events, mouseEvent{Kind: kind, X: math.Round(x), Y: math.Round(y)})
	if len(n.events) > 32 {
		n.events = n.events[len(n.events)-32:]
	}
}

func (n *nativeLive) takeMouse() []mouseEvent {
	n.evMu.Lock()
	defer n.evMu.Unlock()
	out := n.events
	n.events = nil
	return out
}

func jsonString(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

var safeName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func (n *nativeLive) handle(op, raw string) (string, error) {
	start := time.Now()
	n.pageAlive.Store(start.UnixNano())
	n.lastOp.Store(op)
	defer func() {
		if d := time.Since(start); d > 150*time.Millisecond {
			nativeLog.Warn("slow request on the window thread", "op", op, "ms", d.Milliseconds())
		}
	}()
	switch op {
	case "ping":
		return "{}", nil

	case "play":
		var a playArgs
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return "", err
		}
		ref := strings.TrimSpace(a.Ref)
		if !httpapi.ValidServiceRef(ref) {
			return "", errors.New("not a channel reference")
		}
		xid, err := n.xid()
		if err != nil {
			return "", err
		}
		n.applyBox(a.boxArgs, false)
		n.mu.Lock()
		n.gen++
		gen := n.gen
		n.started, n.errMsg, n.startedAt = false, "", time.Now()
		n.volume, n.muted, n.audioLang, n.audioApplied = a.Volume, a.Muted, a.AudioLang, false
		n.opts = playOptions(a)
		n.fit = validFit(a.Fit)
		n.snap = stateOut{State: "opening", AudioCurrent: -1}
		n.mu.Unlock()
		go n.startPlayback(gen, a.Profile, ref, xid)
		return `{"ok":true}`, nil

	case "bounds":
		var b boxArgs
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			return "", err
		}
		n.applyBox(b, b.Visible)
		return "{}", nil

	case "fit":
		var a struct{ Mode string }
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return "", err
		}
		n.mu.Lock()
		n.fit = validFit(a.Mode)
		n.mu.Unlock()
		n.applyFit()
		return "{}", nil

	case "mouse":
		return jsonString(map[string]any{"events": n.takeMouse()})

	case "cursor":
		var a struct{ Hide bool }
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return "", err
		}
		n.setCursorHidden(a.Hide && n.fsFlag.Load())
		return "{}", nil

	case "fullscreen":
		var a struct{ On bool }
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return "", err
		}
		n.setFullscreen(a.On)
		return "{}", nil

	case "stop":
		n.mu.Lock()
		n.gen++
		n.started, n.errMsg = false, ""
		n.snap = stateOut{State: "idle", AudioCurrent: -1}
		n.mu.Unlock()
		n.hideChild()
		n.withPlayer(func(p *vlc.Player) { n.playMu.Lock(); p.Stop(); n.playMu.Unlock() })
		return "{}", nil

	case "pause":
		var a struct{ Paused bool }
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return "", err
		}
		n.withPlayer(func(p *vlc.Player) { p.SetPause(a.Paused) })
		return "{}", nil

	case "volume":
		var a struct {
			Volume int
			Muted  bool
		}
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return "", err
		}
		n.mu.Lock()
		n.volume, n.muted = a.Volume, a.Muted
		n.mu.Unlock()
		n.withPlayer(func(p *vlc.Player) { p.SetVolume(a.Volume); p.SetMute(a.Muted) })
		return "{}", nil

	case "audio":
		var a struct{ ID int }
		if err := json.Unmarshal([]byte(raw), &a); err != nil {
			return "", err
		}
		n.withPlayer(func(p *vlc.Player) { p.SetAudioTrack(a.ID) })
		return "{}", nil

	case "snapshot":
		var a struct{ Name string }
		_ = json.Unmarshal([]byte(raw), &a)
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		dir := filepath.Join(home, "Pictures", "DCC Lite")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "", err
		}
		safe := strings.Trim(safeName.ReplaceAllString(a.Name, "_"), "_.")
		if safe == "" {
			safe = "channel"
		}
		if len(safe) > 40 {
			safe = safe[:40]
		}
		path := filepath.Join(dir, safe+"-"+time.Now().Format("20060102-150405")+".png")
		n.withPlayer(func(p *vlc.Player) {
			if err := p.Snapshot(path); err != nil {
				nativeLog.Warn("snapshot failed", "error", err)
			}
		})
		return jsonString(map[string]string{"path": path})

	case "state":
		n.mu.Lock()
		out := n.snap
		if n.errMsg != "" {
			out = stateOut{State: "error", Error: n.errMsg, AudioCurrent: -1}
		}
		n.mu.Unlock()
		if n.vlcDone.Load() && n.vlcErr != nil && out.State != "idle" {
			out = stateOut{State: "error", Error: n.vlcErr.Error(), AudioCurrent: -1}
		}
		return jsonString(out)
	}
	return "", fmt.Errorf("unknown request %q", op)
}

// playOptions turns what the page asked for into safe player settings.
func playOptions(a playArgs) vlc.PlayOptions {
	o := vlc.PlayOptions{CachingMs: a.Caching, Deinterlace: vlc.DeinterlaceAuto}
	if o.CachingMs < 200 || o.CachingMs > 10000 {
		o.CachingMs = 1500
	}
	switch strings.ToLower(a.Deinterlace) {
	case vlc.DeinterlaceYadif:
		o.Deinterlace = vlc.DeinterlaceYadif
	case vlc.DeinterlaceOff:
		o.Deinterlace = vlc.DeinterlaceOff
	}
	o.NoHardware = a.Hardware != nil && !*a.Hardware
	return o
}

func (n *nativeLive) startPlayback(gen int, id, ref string, xid uintptr) {
	fail := func(msg string) {
		n.mu.Lock()
		if gen == n.gen {
			n.errMsg = msg
		}
		n.mu.Unlock()
	}
	profile, ok := n.app.Profiles.Get(id)
	if !ok {
		fail(msg("nodevice"))
		return
	}
	if !profile.Enabled {
		fail(msg("disabled"))
		return
	}
	p, err := n.vlc()
	if err != nil {
		fail(err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	mrl, ok := httpapi.PlaybackURL(ctx, profile, ref)
	if !ok {
		fail(msg("badaddr"))
		return
	}

	n.playMu.Lock()
	defer n.playMu.Unlock()
	n.mu.Lock()
	if gen != n.gen {
		n.mu.Unlock()
		return
	}
	vol, muted, opts := n.volume, n.muted, n.opts
	n.mu.Unlock()
	p.SetWindow(xid)
	p.SetVolume(vol)
	p.SetMute(muted)
	if err := p.Play(mrl, opts); err != nil {
		fail(err.Error())
		return
	}
	n.mu.Lock()
	if gen == n.gen {
		n.started = true
	}
	n.mu.Unlock()
}

var langInBrackets = regexp.MustCompile(`\[([^\]]+)\]`)

// trackKey makes "Track 2 - [English]" and "Track 1 - [English]" compare equal.
func trackKey(name string) string {
	if m := langInBrackets.FindStringSubmatch(name); m != nil {
		return strings.ToLower(strings.TrimSpace(m[1]))
	}
	return strings.ToLower(strings.TrimSpace(name))
}

// stallAfter is how long a "playing" stream may stand still before the page is told to reconnect.
const stallAfter = 6 * time.Second

// poll refreshes the snapshot the page reads. It is the only place that asks VLC for its state.
func (n *nativeLive) poll() {
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	lastState := ""
	var trackAt, movedAt time.Time
	var lastT int64 = -1
	var moved, deintDone bool
	for range tick.C {
		if n.closed.Load() {
			return
		}
		n.mu.Lock()
		started, since, lang, applied, failed := n.started, time.Since(n.startedAt), n.audioLang, n.audioApplied, n.errMsg != ""
		at := n.startedAt
		n.mu.Unlock()
		if !started || failed || !n.vlcDone.Load() || n.player == nil {
			continue
		}
		p := n.player
		out := stateOut{State: "opening", AudioCurrent: -1}
		switch p.State() {
		case vlc.StateOpening:
			out.State = "opening"
		case vlc.StateBuffering:
			out.State = "buffering"
		case vlc.StatePlaying:
			out.State = "playing"
		case vlc.StatePaused:
			out.State = "paused"
		case vlc.StateEnded:
			out.State = "ended"
		case vlc.StateError:
			out.State = "error"
			out.Error = "VLC could not play this channel. The receiver may be in standby or busy, or the channel is encrypted and no tuner is free."
		default: // idle and stopped: VLC needs a moment before it reports "opening"
			if since < 15*time.Second {
				out.State = "opening"
			} else {
				out.State = "stopped"
			}
		}
		if out.State == "playing" || out.State == "paused" || out.State == "buffering" {
			tracks, cur := p.AudioTracks()
			for _, t := range tracks {
				out.Audio = append(out.Audio, trackOut{ID: t.ID, Name: t.Name})
			}
			out.AudioCurrent = cur
			if !applied && len(tracks) > 0 {
				n.mu.Lock()
				n.audioApplied = true
				n.mu.Unlock()
				if want := trackKey(lang); want != "" {
					for _, t := range tracks {
						if trackKey(t.Name) == want {
							if t.ID != cur {
								p.SetAudioTrack(t.ID)
								out.AudioCurrent = t.ID
							}
							break
						}
					}
				}
			}
			out.Width, out.Height = p.VideoSize()
		}
		now := time.Now()
		if !at.Equal(trackAt) { // a new channel: start watching afresh
			trackAt, lastT, moved, movedAt, deintDone = at, -1, false, now, false
		}
		if out.State == "playing" {
			if t := p.Time(); t >= 0 {
				if t != lastT {
					moved = moved || lastT >= 0
					lastT, movedAt = t, now
				} else if moved && now.Sub(movedAt) > stallAfter {
					nativeLog.Warn("the picture froze; reconnecting", "still_ms", now.Sub(movedAt).Milliseconds(), "video", fmt.Sprintf("%dx%d", out.Width, out.Height))
					out.State = "stalled"
					moved, movedAt = false, now
				}
			}
			if !deintDone && out.Width > 0 { // the first frame exists: now the deinterlace choice can be applied
				deintDone = true
				p.ApplyDeinterlace()
				n.mu.Lock()
				mode, fw, fh := n.fit, n.last.w, n.last.h
				n.mu.Unlock()
				p.SetFit(mode, fw, fh)
			}
		} else {
			movedAt = now // paused, buffering or opening: the clock starts again
		}
		if out.State != lastState {
			lastState = out.State
			nativeLog.Info("vlc state", "state", out.State, "video", fmt.Sprintf("%dx%d", out.Width, out.Height))
		}
		n.mu.Lock()
		if n.started { // not stopped or replaced while VLC was being asked
			n.snap = out
		}
		n.mu.Unlock()
	}
}

func (n *nativeLive) watchdog() {
	var uiBad, pageBad bool
	var lastWarn time.Time
	for !n.closed.Load() {
		time.Sleep(time.Second)
		if nativeUp.Load() {
			C.dcc_cocoa_ping()
		}
		time.Sleep(1500 * time.Millisecond)
		if !nativeUp.Load() {
			continue
		}
		now := time.Now()
		uiAge := now.Sub(time.Unix(0, n.uiAlive.Load()))
		pageAge := now.Sub(time.Unix(0, n.pageAlive.Load()))
		n.mu.Lock()
		live := n.started
		n.mu.Unlock()
		ui := uiAge > 3*time.Second
		page := live && pageAge > 4*time.Second
		switch {
		case (ui || page) && now.Sub(lastWarn) > 5*time.Second:
			lastWarn = now
			op, _ := n.lastOp.Load().(string)
			nativeLog.Warn("watchdog: not responding", "window_thread_quiet_s", int(uiAge.Seconds()), "page_quiet_s", int(pageAge.Seconds()), "last_request", op)
		case !ui && !page && (uiBad || pageBad):
			nativeLog.Info("watchdog: responding again")
		}
		uiBad, pageBad = ui, page
	}
}
