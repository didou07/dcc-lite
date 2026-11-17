//go:build windows && native

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/bits"
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

	procGetCursorPos       = user32.NewProc("GetCursorPos")
	procGetAsyncKeyState   = user32.NewProc("GetAsyncKeyState")
	procCreateCursor       = user32.NewProc("CreateCursor")
	procLoadCursor         = user32.NewProc("LoadCursorW")
	procSendMessage        = user32.NewProc("SendMessageW")
	procSetClassLongPtr    = user32.NewProc(classLongName())
	procWindowFromPoint    = user32.NewProc("WindowFromPoint")
	procGetAncestor        = user32.NewProc("GetAncestor")
	procGetDoubleClickTime = user32.NewProc("GetDoubleClickTime")
	procGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	procSetWindowLong      = user32.NewProc("SetWindowLongW")
	procGetWindowPlacement = user32.NewProc("GetWindowPlacement")
	procSetWindowPlacement = user32.NewProc("SetWindowPlacement")
	procMonitorFromWindow  = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfo     = user32.NewProc("GetMonitorInfoW")
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

	vkLButton     = 0x01
	vkRButton     = 0x02
	smSwapButton  = 23
	idcArrow      = 32512
	wmSetCursor   = 0x0020
	gclpHCursor   = ^uintptr(11) // -12
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	gaRoot        = 2
	smCxDoubleClk = 36
	smCyDoubleClk = 37

	gwlStyle             = ^uintptr(15) // -16
	wsOverlappedWindow   = 0x00CF0000
	swpFrameChanged      = 0x0020
	swpNoOwnerZOrder     = 0x0200
	monitorDefaultNearer = 2
)

type winRect struct{ Left, Top, Right, Bottom int32 }

// SetClassLongPtrW does not exist in 32-bit Windows, where SetClassLongW is the same call.
func classLongName() string {
	if bits.UintSize == 64 {
		return "SetClassLongPtrW"
	}
	return "SetClassLongW"
}

type winPoint struct{ X, Y int32 }

// winPlacement is WINDOWPLACEMENT and monitorInfo is MONITORINFO.
type winPlacement struct {
	Length, Flags, ShowCmd uint32
	MinPos, MaxPos         winPoint
	Normal                 winRect
}
type monitorInfo struct {
	Size          uint32
	Monitor, Work winRect
	Flags         uint32
}
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

func fixedRuntime(dataDir string) string {
	var roots []string
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(exe))
	}
	roots = append(roots, dataDir)
	for _, root := range roots {
		for _, name := range []string{"WebView2", "webview2-runtime", "WebView2Runtime"} {
			base := filepath.Join(root, name)
			if _, err := os.Stat(filepath.Join(base, "msedgewebview2.exe")); err == nil {
				return base
			}
			subs, _ := os.ReadDir(base)
			for _, s := range subs {
				if s.IsDir() {
					dir := filepath.Join(base, s.Name())
					if _, err := os.Stat(filepath.Join(dir, "msedgewebview2.exe")); err == nil {
						return dir
					}
				}
			}
		}
	}
	return ""
}

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

func browserFallback(d nativeDeps) error {
	openBrowser(d.URL)
	messageBox(msg("fallback")+"\n\n"+d.URL, mbIconInfo)
	return nil
}

// runNative shows the window and returns when it is closed.
func runNative(d nativeDeps) error {
	if dir := fixedRuntime(d.DataDir); dir != "" {
		_ = os.Setenv("WEBVIEW2_BROWSER_EXECUTABLE_FOLDER", dir)
		nativeLog.Info("using the WebView2 runtime shipped with the app", "folder", dir)
	} else if !webView2Installed() {
		nativeLog.Warn("no WebView2 runtime found; opening the interface in the default browser", "url", d.URL)
		return browserFallback(d)
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
		nativeLog.Warn("the WebView2 window could not be created; opening the interface in the default browser", "url", d.URL)
		return browserFallback(d)
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

	view.Init(fmt.Sprintf(`window.__dccGui = %q;`, strings.TrimRight(d.URL, "/")))
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
	opts         vlc.PlayOptions // the player settings of the running channel
	snap         stateOut
	fit          string      // how the picture fills its window (vlc.Fit...)
	fitTimer     *time.Timer // debounces a re-fit after the box changed size
	lastVW       float64     // the page's viewport size at the last box, to turn screen pixels back into CSS pixels
	lastVH       float64
	cursorHidden atomic.Bool
	events       []mouseEvent // mouse events for the page, newest last
	evMu         sync.Mutex
	evLogs       int
	mouseLogs    int
	fsFlag       atomic.Bool // fsOn, readable from the mouse hook
	fsOn         bool        // the window is borderless over the whole screen; used on the window thread only
	fsStyle      uintptr
	fsPlace      winPlacement
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
		go n.watchMouse()
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
	resized := l.w != n.last.w || l.h != n.last.h
	n.last = l
	n.lastVW, n.lastVH = b.VW, b.VH
	n.mu.Unlock()
	n.place(true)
	if resized {
		n.refit()
	}
}

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
		if cur.show {
			n.stack(host)
		}
		return
	}
	n.mu.Lock()
	n.applied = cur
	n.mu.Unlock()
	if cur.show != prev.show {
		nativeLog.Info("video window", "show", cur.show, "screen", fmt.Sprintf("%d,%d %dx%d", cur.x, cur.y, cur.w, cur.h))
	}
	flags := uintptr(swpNoActivate | swpHideWindow | swpAsyncPos | swpNoZOrder)
	var after uintptr
	if cur.show {
		flags = swpNoActivate | swpShowWindow | swpAsyncPos
		var change bool
		if after, change = n.insertAfter(host); !change {
			flags |= swpNoZOrder
		}
	}
	_, _, _ = procSetWindowPos.Call(host, after, uintptr(cur.x), uintptr(cur.y), uintptr(cur.w), uintptr(cur.h), flags)
	// VLC's own video window lives inside ours on yet another thread. Make it fill ours on every
	// change rather than relying on VLC to notice.
	if cur.show {
		if inner, _, _ := procGetWindow.Call(host, gwChild); inner != 0 {
			_, _, _ = procSetWindowPos.Call(inner, 0, 0, 0, uintptr(cur.w), uintptr(cur.h), swpNoZOrder|swpNoActivate|swpAsyncPos)
		}
	}
}

func (n *nativeLive) insertAfter(host uintptr) (after uintptr, change bool) {
	above, _, _ := procGetWindow.Call(n.parent, gwHwndPrev)
	if above == host {
		return 0, false
	}
	if above == 0 { // the page window is the top one
		if ex, _, _ := procGetWindowLong.Call(n.parent, gwlExStyle); ex&wsExTopmost != 0 {
			return ^uintptr(0), true // HWND_TOPMOST: the page window is always-on-top, so is the picture
		}
		return 0, true // HWND_TOP
	}
	return above, true
}

// stack puts the picture window back directly above the page window when something else has been
// raised between them or above both.
func (n *nativeLive) stack(host uintptr) {
	after, change := n.insertAfter(host)
	if !change {
		return
	}
	_, _, _ = procSetWindowPos.Call(host, after, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate|swpNoOwnerZ|swpAsyncPos)
}

type mouseEvent struct {
	Kind string  `json:"k"`
	X    float64 `json:"x"`
	Y    float64 `json:"y"`
}

func doubleClickTime() time.Duration {
	r, _, _ := procGetDoubleClickTime.Call()
	if r < 100 || r > 5000 {
		r = 500
	}
	return time.Duration(r) * time.Millisecond
}

func absInt32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func (n *nativeLive) overPicture(pt winPoint) bool {
	n.mu.Lock()
	a, host := n.applied, n.child
	n.mu.Unlock()
	if host == 0 || !a.show || pt.X < a.x || pt.Y < a.y || pt.X >= a.x+a.w || pt.Y >= a.y+a.h {
		return false
	}
	var w uintptr
	if bits.UintSize == 64 {
		packed := uint64(uint32(pt.X)) | uint64(uint32(pt.Y))<<32 // POINT travels as one 64-bit value
		w, _, _ = procWindowFromPoint.Call(uintptr(packed))
	} else {
		w, _, _ = procWindowFromPoint.Call(uintptr(pt.X), uintptr(pt.Y))
	}
	if w == 0 {
		return false
	}
	root, _, _ := procGetAncestor.Call(w, gaRoot)
	if root != host && root != n.parent {
		if n.mouseLogs < 4 {
			n.mouseLogs++
			nativeLog.Info("the picture window is not under the mouse", "under", fmt.Sprintf("%x", w), "root", fmt.Sprintf("%x", root), "picture", fmt.Sprintf("%x", host))
		}
		return false
	}
	return true
}

// cssPoint turns a screen point into the page's own CSS pixels.
func (n *nativeLive) cssPoint(pt winPoint) (float64, float64) {
	n.mu.Lock()
	vw, vh := n.lastVW, n.lastVH
	n.mu.Unlock()
	var origin winPoint
	_, _, _ = procClientToScreen.Call(n.parent, uintptr(unsafe.Pointer(&origin)))
	var rc winRect
	_, _, _ = procGetClientRect.Call(n.parent, uintptr(unsafe.Pointer(&rc)))
	x, y := float64(pt.X-origin.X), float64(pt.Y-origin.Y)
	if cw, ch := float64(rc.Right-rc.Left), float64(rc.Bottom-rc.Top); cw > 0 && ch > 0 && vw > 0 && vh > 0 {
		x, y = x*vw/cw, y*vh/ch
	}
	return math.Round(x), math.Round(y)
}

func (n *nativeLive) pushMouse(kind string, pt winPoint) {
	x, y := n.cssPoint(pt)
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
	n.events = append(n.events, mouseEvent{Kind: kind, X: x, Y: y})
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

func keyDown(vk uintptr) bool {
	r, _, _ := procGetAsyncKeyState.Call(vk)
	return r&0x8000 != 0
}

func (n *nativeLive) watchMouse() {
	var lastAt time.Time
	var lastPt, lastPos winPoint
	var prevL, prevR, rightBegan bool
	for !n.closed.Load() {
		n.mu.Lock()
		show := n.applied.show
		n.mu.Unlock()
		if !show {
			prevL, prevR, rightBegan = false, false, false
			time.Sleep(60 * time.Millisecond)
			continue
		}
		var pt winPoint
		_, _, _ = procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
		left, right := uintptr(vkLButton), uintptr(vkRButton)
		if sw, _, _ := procGetSystemMetrics.Call(smSwapButton); sw != 0 {
			left, right = right, left // the buttons are swapped in the mouse settings
		}
		l, r := keyDown(left), keyDown(right)
		inside := n.overPicture(pt)
		if pt != lastPos {
			lastPos = pt
			if inside && n.fsFlag.Load() {
				if n.cursorHidden.Load() {
					n.setCursorHidden(false)
				}
				n.pushMouse("move", pt)
			}
		}
		if l && !prevL {
			now := time.Now()
			cx, _, _ := procGetSystemMetrics.Call(smCxDoubleClk)
			cy, _, _ := procGetSystemMetrics.Call(smCyDoubleClk)
			switch {
			case !inside:
				lastAt = time.Time{}
			case !lastAt.IsZero() && now.Sub(lastAt) <= doubleClickTime() &&
				absInt32(pt.X-lastPt.X) <= int32(cx/2)+2 && absInt32(pt.Y-lastPt.Y) <= int32(cy/2)+2:
				lastAt = time.Time{}
				n.pushMouse("dbl", pt)
			default:
				lastAt, lastPt = now, pt
			}
		}
		if r && !prevR {
			rightBegan = inside
		}
		if !r && prevR && rightBegan {
			rightBegan = false
			if inside {
				n.pushMouse("menu", pt)
			}
		}
		prevL, prevR = l, r
		time.Sleep(15 * time.Millisecond)
	}
}

func (n *nativeLive) setCursorHidden(hide bool) {
	n.mu.Lock()
	host := n.child
	n.mu.Unlock()
	if host == 0 {
		return
	}
	cur := uintptr(0)
	if hide {
		andMask := make([]byte, 128)
		for i := range andMask {
			andMask[i] = 0xFF
		}
		xorMask := make([]byte, 128)
		var inst windows.Handle
		_ = windows.GetModuleHandleEx(0, nil, &inst)
		cur, _, _ = procCreateCursor.Call(uintptr(inst), 0, 0, 32, 32, uintptr(unsafe.Pointer(&andMask[0])), uintptr(unsafe.Pointer(&xorMask[0])))
	} else {
		cur, _, _ = procLoadCursor.Call(0, idcArrow)
	}
	if cur == 0 {
		return
	}
	n.cursorHidden.Store(hide)
	inner, _, _ := procGetWindow.Call(host, gwChild)
	for _, w := range []uintptr{host, inner} {
		if w == 0 {
			continue
		}
		_, _, _ = procSetClassLongPtr.Call(w, gclpHCursor, cur)
		_, _, _ = procSendMessage.Call(w, wmSetCursor, w, 1|0x0200<<16) // HTCLIENT, WM_MOUSEMOVE
	}
}

// ---- Full screen ----

func (n *nativeLive) setFullscreen(on bool) {
	n.view.Dispatch(func() {
		hwnd := n.parent
		if on == n.fsOn {
			return
		}
		if on {
			mon, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultNearer)
			mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
			if r, _, _ := procGetMonitorInfo.Call(mon, uintptr(unsafe.Pointer(&mi))); r == 0 {
				return
			}
			n.fsPlace = winPlacement{Length: uint32(unsafe.Sizeof(winPlacement{}))}
			_, _, _ = procGetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&n.fsPlace)))
			style, _, _ := procGetWindowLong.Call(hwnd, gwlStyle)
			n.fsStyle = style
			_, _, _ = procSetWindowLong.Call(hwnd, gwlStyle, style&^uintptr(wsOverlappedWindow))
			_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(mi.Monitor.Left), uintptr(mi.Monitor.Top),
				uintptr(mi.Monitor.Right-mi.Monitor.Left), uintptr(mi.Monitor.Bottom-mi.Monitor.Top),
				swpNoOwnerZOrder|swpFrameChanged)
			n.fsOn = true
			n.fsFlag.Store(true)
			return
		}
		_, _, _ = procSetWindowLong.Call(hwnd, gwlStyle, n.fsStyle)
		_, _, _ = procSetWindowPlacement.Call(hwnd, uintptr(unsafe.Pointer(&n.fsPlace)))
		_, _, _ = procSetWindowPos.Call(hwnd, 0, 0, 0, 0, 0,
			swpNoMove|swpNoSize|swpNoZOrder|swpNoOwnerZ|swpNoActivate|swpFrameChanged)
		n.fsOn = false
		n.fsFlag.Store(false)
		if n.cursorHidden.Load() {
			go n.setCursorHidden(false)
		}
	})
}

// ---- Fit: how the picture fills its window ----

func validFit(mode string) string {
	switch mode {
	case vlc.FitFill, vlc.FitStretch, vlc.Fit169, vlc.Fit43:
		return mode
	}
	return vlc.FitOriginal
}

// applyFit gives the running picture the current fit for the window's present size.
func (n *nativeLive) applyFit() {
	n.mu.Lock()
	mode, w, h := n.fit, n.last.w, n.last.h
	n.mu.Unlock()
	n.withPlayer(func(p *vlc.Player) { p.SetFit(mode, int(w), int(h)) })
}

// refit repeats the fit a moment after the window stopped changing size: only fill and stretch
// depend on the window's shape.
func (n *nativeLive) refit() {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.fit != vlc.FitFill && n.fit != vlc.FitStretch {
		return
	}
	if n.fitTimer != nil {
		n.fitTimer.Stop()
	}
	n.fitTimer = time.AfterFunc(200*time.Millisecond, n.applyFit)
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
		n.opts = playOptions(a)
		n.fit = validFit(a.Fit)
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
	p.SetWindow(child)
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
				p.SetFit(mode, int(fw), int(fh))
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
