//go:build windows && native

package main

import (
	"sync"
	"sync/atomic"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	whMouseLL    = 14
	wmMouseWheel = 0x020A
)

var (
	procSetWindowsHookEx    = user32.NewProc("SetWindowsHookExW")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")

	wheelTarget atomic.Pointer[nativeLive]
	wheelOnce   sync.Once
	wheelCb     uintptr
)

type msllHook struct {
	Pt    winPoint
	Data  uint32
	Flags uint32
	Time  uint32
	Extra uintptr
}

func wheelHook(code, wParam, lParam uintptr) uintptr {
	if int32(code) >= 0 && wParam == wmMouseWheel && lParam != 0 {
		if n := wheelTarget.Load(); n != nil && !n.closed.Load() {
			ms := (*msllHook)(unsafe.Pointer(lParam))
			n.pushWheel(ms.Pt, int16(ms.Data>>16))
		}
	}
	r, _, _ := procCallNextHookEx.Call(0, code, wParam, lParam)
	return r
}

// installWheelHook must run on the thread that pumps messages.
func (n *nativeLive) installWheelHook() func() {
	wheelOnce.Do(func() { wheelCb = windows.NewCallback(wheelHook) })
	wheelTarget.Store(n)
	h, _, _ := procSetWindowsHookEx.Call(whMouseLL, wheelCb, 0, 0)
	if h == 0 {
		return func() {}
	}
	return func() {
		_, _, _ = procUnhookWindowsHookEx.Call(h)
		wheelTarget.CompareAndSwap(n, nil)
	}
}

func (n *nativeLive) pushWheel(pt winPoint, delta int16) {
	if delta == 0 || !n.overPicture(pt) {
		return
	}
	y := 1.0
	if delta > 0 {
		y = -1
	}
	n.evMu.Lock()
	defer n.evMu.Unlock()
	n.events = append(n.events, mouseEvent{Kind: "wheel", Y: y})
	if len(n.events) > 32 {
		n.events = n.events[len(n.events)-32:]
	}
}
