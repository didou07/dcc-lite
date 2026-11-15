//go:build windows && native

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/httpapi"
	"github.com/didou07/dcc-lite/src/logx"
	"github.com/didou07/dcc-lite/src/vlc"
)

// The Windows app: the same interface as the browser one, shown in a WebView2 window, with live TV
// played by the user's own VLC in a native window laid over the page's video box.
const nativeBuild = true

var nativeLog = logx.For("native")

func init() {
	// The window and its message loop must stay on the program's first thread.
	runtime.LockOSThread()
}

var (
	user32             = windows.NewLazySystemDLL("user32.dll")
	procMessageBoxW    = user32.NewProc("MessageBoxW")
	procCreateWindowEx = user32.NewProc("CreateWindowExW")
	procSetWindowPos   = user32.NewProc("SetWindowPos")
	procGetClientRect  = user32.NewProc("GetClientRect")
	procSetDPIAware    = user32.NewProc("SetProcessDPIAware")
	procGetWindow      = user32.NewProc("GetWindow")
	procClientToScreen = user32.NewProc("ClientToScreen")
	procIsIconic       = user32.NewProc("IsIconic")
	procGetWindowLong  = user32.NewProc("GetWindowLongW")
	procGetMessage     = user32.NewProc("GetMessageW")
	procTranslateMsg   = user32.NewProc("TranslateMessage")
	procDispatchMsg    = user32.NewProc("DispatchMessageW")
)

const (
	wsPopup        = 0x80000000
	wsExToolWindow = 0x00000080
	wsExNoActivate = 0x08000000
	wsClipSiblings = 0x04000000
	wsClipChildren = 0x02000000
	ssBlackRect    = 0x00000004
	swpNoActivate  = 0x0010
	swpShowWindow  = 0x0040
	swpHideWindow  = 0x0080
	swpNoZOrder    = 0x0004
	swpAsyncPos    = 0x4000 // post the request instead of waiting for another thread
	gwChild        = 5
	gwHwndPrev     = 3
	gwlExStyle     = ^uintptr(19) // -20
	wsExTopmost    = 0x00000008
	swpNoMove      = 0x0002
	swpNoSize      = 0x0001
	swpNoOwnerZ    = 0x0200
	mbIconInfo     = 0x40
	mbIconError    = 0x10
)

type winRect struct{ Left, Top, Right, Bottom int32 }
type winPoint struct{ X, Y int32 }
type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	X, Y    int32
}

// placed is a rectangle for the video window: relative to the page's window in last, in screen
// coordinates in applied.
type placed struct {
	x, y, w, h int32
	show, ok   bool
}

func messageBox(text string, icon uintptr) {
	t, _ := windows.UTF16PtrFromString(text)
	c, _ := windows.UTF16PtrFromString("DCC Lite")
	_, _, _ = procMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), icon)
}

func nativeNotice(text string) { messageBox(text, mbIconInfo) }

// webView2Installed looks for the Edge WebView2 Runtime. Without it the window cannot open, and the
// library leaves an empty window behind, so it is checked before anything is created.
func webView2Installed() bool {
	const id = `{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	places := []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\Microsoft\EdgeUpdate\Clients\` + id},
		{registry.LOCAL_MACHINE, `SOFTWARE\Microsoft\EdgeUpdate\Clients\` + id},
		{registry.CURRENT_USER, `Software\Microsoft\EdgeUpdate\Clients\` + id},
	}
	for _, p := range places {
		k, err := registry.OpenKey(p.root, p.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		pv, _, err := k.GetStringValue("pv")
		k.Close()
		if err == nil && pv != "" && pv != "0.0.0.0" {
			return true
		}
	}
	return false
}

var (
	nativeMu   sync.Mutex
	nativeView webview2.WebView
)

// nativeClose asks the window to close. It is safe to call from any goroutine.
func nativeClose() {
	nativeMu.Lock()
	v := nativeView
	nativeMu.Unlock()
	if v != nil {
		v.Destroy()
	}
}

// runNative shows the window and returns when it is closed.
func runNative(d nativeDeps) error {
	if !webView2Installed() {
		messageBox("DCC Lite needs the Microsoft Edge WebView2 Runtime, which is not installed on this computer.\n\n"+
			"Install it from microsoft.com/edge/webview2, or use the DCCLite.exe version that opens in your browser.", mbIconError)
		return errors.New("WebView2 Runtime is not installed")
	}
	_, _, _ = procSetDPIAware.Call() // crisp text on scaled displays; must come before the window exists

	view := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  filepath.Join(d.DataDir, "webview"),
		WindowOptions: webview2.WindowOptions{
			Title: "DCC Lite", Width: 1280, Height: 820, Center: true,
		},
	})
	if view == nil {
		messageBox("The DCC Lite window could not be created.", mbIconError)
		return errors.New("webview window could not be created")
	}
	nativeMu.Lock()
	nativeView = view
	nativeMu.Unlock()
	defer view.Destroy()

	view.SetTitle("DCC Lite")
	view.SetSize(960, 620, webview2.HintMin)

	live := &nativeLive{app: d.App, view: view, parent: uintptr(view.Window()), snap: stateOut{State: "opening", AudioCurrent: -1}}
	go func() { _, _ = live.vlc() }() // load VLC in the background so the first channel starts fast
	go func() { _ = live.ensureChild() }()
	go live.poll()
	go live.follow()
	go live.watchdog()
	defer live.close()

	if err := view.Bind("dccLive", live.handle); err != nil {
		return err
	}
	// Only this program's own pages may drive the player.
	view.Init(fmt.Sprintf(`if (location.origin !== %q) { try { delete window.dccLive; } catch (e) {} }`, strings.TrimRight(d.URL, "/")))
	view.Navigate(d.URL)
	view.Run()
	return nil
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

type nativeLive struct {
	app    *app.App
	view   webview2.WebView
	parent uintptr

	vlcOnce sync.Once
	vlcDone atomic.Bool // set when loading VLC has finished, successfully or not
	lib     *vlc.Lib
	player  *vlc.Player
	vlcErr  error

	playMu  sync.Mutex // one start of the player at a time
	childMu sync.Mutex // one creation of the picture window at a time

	mu           sync.Mutex
	child        uintptr
	last         placed
	applied      placed // where the picture window was last put, in screen pixels
	gen          int
	started      bool
	errMsg       string
	startedAt    time.Time
	volume       int
	muted        bool
	audioLang    string
	audioApplied bool
	snap         stateOut
	boxLogs      int
	closed       atomic.Bool
	resizingTill atomic.Int64
	uiAlive      atomic.Int64
	pageAlive    atomic.Int64 // unix nanos: last time the page called in
	lastOp       atomic.Value // the request being handled, for the watchdog
}

func (n *nativeLive) vlc() (*vlc.Player, error) {
	n.vlcOnce.Do(func() {
		defer n.vlcDone.Store(true)
		lib, err := vlc.Load()
		if err != nil {
			n.vlcErr = err
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
		nativeLog.Info("VLC loaded", "dir", lib.Dir())
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

// ensureChild creates the picture window. It must have no owner and no parent: either one ties its
// thread's input queue to the page window's and a VLC stall would freeze the whole interface.
func (n *nativeLive) ensureChild() error {
	n.childMu.Lock() // the start-up call and the first play must not each make a window
	defer n.childMu.Unlock()
	n.mu.Lock()
	have := n.child != 0
	n.mu.Unlock()
	if have {
		return nil
	}
	type result struct {
		h   uintptr
		err error
	}
	done := make(chan result, 1)
	go func() {
		runtime.LockOSThread() // the window and its message loop live and die on this thread
		class, _ := windows.UTF16PtrFromString("STATIC")
		empty, _ := windows.UTF16PtrFromString("")
		var inst windows.Handle
		_ = windows.GetModuleHandleEx(0, nil, &inst)
		h, _, err := procCreateWindowEx.Call(wsExToolWindow|wsExNoActivate, uintptr(unsafe.Pointer(class)), uintptr(unsafe.Pointer(empty)),
			wsPopup|wsClipChildren|ssBlackRect, 0, 0, 1, 1, 0, 0, uintptr(inst), 0)
		if h == 0 {
			done <- result{err: err}
			return
		}
		done <- result{h: h}
		var msg winMsg
		for {
			r, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
			if int32(r) <= 0 {
				return
			}
			_, _, _ = procTranslateMsg.Call(uintptr(unsafe.Pointer(&msg)))
			_, _, _ = procDispatchMsg.Call(uintptr(unsafe.Pointer(&msg)))
		}
	}()
	select {
	case r := <-done:
		if r.h == 0 {
			return fmt.Errorf("the video window could not be created: %w", r.err)
		}
		n.mu.Lock()
		n.child = r.h
		n.mu.Unlock()
		return nil
	case <-time.After(5 * time.Second):
		return errors.New("the video window did not start")
	}
}

func (n *nativeLive) iconic() bool {
	r, _, _ := procIsIconic.Call(n.parent)
	return r != 0
}

// applyBox records where the page wants the picture and moves the window there. The page measures
// in CSS pixels of its own viewport, so the scale is worked out from the real client area.
func (n *nativeLive) applyBox(b boxArgs, show bool) {
	n.mu.Lock()
	child := n.child
	logIt := n.boxLogs < 8
	if logIt {
		n.boxLogs++
	}
	n.mu.Unlock()
	if child == 0 || b.VW <= 0 || b.VH <= 0 {
		return
	}
	var rc winRect
	_, _, _ = procGetClientRect.Call(n.parent, uintptr(unsafe.Pointer(&rc)))
	sx := float64(rc.Right-rc.Left) / b.VW
	sy := float64(rc.Bottom-rc.Top) / b.VH
	l := placed{
		x: int32(math.Round(b.X * sx)), y: int32(math.Round(b.Y * sy)),
		w: int32(math.Round(b.W * sx)), h: int32(math.Round(b.H * sy)), show: show, ok: true,
	}
	if l.w < 1 || l.h < 1 {
		l.show, l.w, l.h = false, 1, 1
	}
	if logIt {
		nativeLog.Info("video box", "css", fmt.Sprintf("%.0f,%.0f %.0fx%.0f in %.0fx%.0f", b.X, b.Y, b.W, b.H, b.VW, b.VH),
			"client", fmt.Sprintf("%dx%d", rc.Right-rc.Left, rc.Bottom-rc.Top), "placed", fmt.Sprintf("%d,%d %dx%d", l.x, l.y, l.w, l.h), "show", l.show)
	}
	n.mu.Lock()
	n.last = l
	n.mu.Unlock()
	n.place(true)
}

// place moves the picture window to the last requested box, in screen coordinates. Requests are
// posted (SWP_ASYNCWINDOWPOS) because the window belongs to another thread, and this one never waits.
func (n *nativeLive) place(force bool) {
	n.mu.Lock()
	host, l, prev := n.child, n.last, n.applied
	n.mu.Unlock()
	if host == 0 || !l.ok {
		return
	}
	if n.iconic() {
		// No owner any more, so the picture window no longer minimises with the page window.
		if prev.show {
			prev.show = false
			n.mu.Lock()
			n.applied = prev
			n.mu.Unlock()
			_, _, _ = procSetWindowPos.Call(host, 0, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoZOrder|swpNoActivate|swpHideWindow|swpAsyncPos)
		}
		return
	}
	var origin winPoint
	_, _, _ = procClientToScreen.Call(n.parent, uintptr(unsafe.Pointer(&origin)))
	cur := placed{x: origin.X + l.x, y: origin.Y + l.y, w: l.w, h: l.h, show: l.show, ok: true}
	if time.Now().UnixNano() < n.resizingTill.Load() {
		cur.show = false
	}
	if !force && cur == prev {
		return
	}
	n.mu.Lock()
	n.applied = cur
	n.mu.Unlock()
	if cur.show != prev.show {
		nativeLog.Info("video window", "show", cur.show, "screen", fmt.Sprintf("%d,%d %dx%d", cur.x, cur.y, cur.w, cur.h))
	}
	flags := uintptr(swpNoActivate | swpHideWindow | swpAsyncPos)
	if cur.show {
		flags = swpNoActivate | swpShowWindow | swpAsyncPos
	}
	// HWND_TOP (0); stack() then slots it directly above the page window.
	_, _, _ = procSetWindowPos.Call(host, 0, uintptr(cur.x), uintptr(cur.y), uintptr(cur.w), uintptr(cur.h), flags)
	// VLC's own video window lives inside ours on yet another thread. Make it fill ours on every
	// change rather than relying on VLC to notice.
	if cur.show {
		if inner, _, _ := procGetWindow.Call(host, gwChild); inner != 0 {
			_, _, _ = procSetWindowPos.Call(inner, 0, 0, 0, uintptr(cur.w), uintptr(cur.h), swpNoZOrder|swpNoActivate|swpAsyncPos)
		}
		n.stack(host)
	}
}

func (n *nativeLive) stack(host uintptr) {
	above, _, _ := procGetWindow.Call(n.parent, gwHwndPrev)
	if above == host {
		return
	}
	if above == 0 { // the page window is the top one
		above = 0 // HWND_TOP
		if ex, _, _ := procGetWindowLong.Call(n.parent, gwlExStyle); ex&wsExTopmost != 0 {
			above = ^uintptr(0) // HWND_TOPMOST: the page window is always-on-top, so is the picture
		}
	}
	_, _, _ = procSetWindowPos.Call(host, above, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpNoOwnerZ|swpAsyncPos)
}

func (n *nativeLive) follow() {
	tick := time.NewTicker(25 * time.Millisecond)
	defer tick.Stop()
	var lastW, lastH int32
	for range tick.C {
		if n.closed.Load() {
			return
		}
		if n.iconic() {
			continue
		}
		var rc winRect
		_, _, _ = procGetClientRect.Call(n.parent, uintptr(unsafe.Pointer(&rc)))
		w, h := rc.Right-rc.Left, rc.Bottom-rc.Top
		if w > 0 && h > 0 && lastW > 0 && (w != lastW || h != lastH) {
			n.resizingTill.Store(time.Now().Add(250 * time.Millisecond).UnixNano())
		}
		if w > 0 && h > 0 {
			lastW, lastH = w, h
		}
		if till := n.resizingTill.Load(); till != 0 {
			if time.Now().UnixNano() < till {
				n.place(false) // keeps it hidden
				continue
			}
			n.resizingTill.Store(0)
			n.place(true) // settled: show it again at the page's latest box
			continue
		}
		n.place(false)
	}
}

func (n *nativeLive) hideChild() {
	n.mu.Lock()
	n.last.show = false
	host := n.child
	n.mu.Unlock()
	if host != 0 {
		n.place(true)
	}
}

func jsonString(v any) (string, error) {
	b, err := json.Marshal(v)
	return string(b), err
}

// handle is the one function the page can call: dccLive(op, json) -> json. It runs on the window's
// thread and must return quickly without touching VLC.
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
		if err := n.ensureChild(); err != nil {
			return "", err
		}
		n.applyBox(a.boxArgs, false)
		n.mu.Lock()
		n.gen++
		gen, child := n.gen, n.child
		n.started, n.errMsg, n.startedAt = false, "", time.Now()
		n.volume, n.muted, n.audioLang, n.audioApplied = a.Volume, a.Muted, a.AudioLang, false
		n.snap = stateOut{State: "opening", AudioCurrent: -1}
		n.mu.Unlock()
		go n.startPlayback(gen, a.Profile, ref, child)
		return `{"ok":true}`, nil

	case "bounds":
		var b boxArgs
		if err := json.Unmarshal([]byte(raw), &b); err != nil {
			return "", err
		}
		n.applyBox(b, b.Visible)
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
		safe := strings.Trim(regexp.MustCompile(`[^A-Za-z0-9._-]+`).ReplaceAllString(a.Name, "_"), "_.")
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

func (n *nativeLive) startPlayback(gen int, id, ref string, child uintptr) {
	fail := func(msg string) {
		n.mu.Lock()
		if gen == n.gen {
			n.errMsg = msg
		}
		n.mu.Unlock()
	}
	profile, ok := n.app.Profiles.Get(id)
	if !ok {
		fail("The device was not found.")
		return
	}
	if !profile.Enabled {
		fail("This device is disabled.")
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
		fail("The channel address is not usable.")
		return
	}

	n.playMu.Lock()
	defer n.playMu.Unlock()
	n.mu.Lock()
	if gen != n.gen {
		n.mu.Unlock()
		return
	}
	vol, muted := n.volume, n.muted
	n.mu.Unlock()
	p.SetWindow(child)
	p.SetVolume(vol)
	p.SetMute(muted)
	if err := p.Play(mrl); err != nil {
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

// poll refreshes the snapshot the page reads. It is the only place that asks VLC for its state.
func (n *nativeLive) poll() {
	tick := time.NewTicker(300 * time.Millisecond)
	defer tick.Stop()
	lastState := ""
	for range tick.C {
		if n.closed.Load() {
			return
		}
		n.mu.Lock()
		started, since, lang, applied, failed := n.started, time.Since(n.startedAt), n.audioLang, n.audioApplied, n.errMsg != ""
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
	n.uiAlive.Store(time.Now().UnixNano())
	var uiBad, pageBad bool
	var lastWarn time.Time
	for !n.closed.Load() {
		time.Sleep(time.Second)
		n.view.Dispatch(func() { n.uiAlive.Store(time.Now().UnixNano()) })
		time.Sleep(1500 * time.Millisecond)
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
