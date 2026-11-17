//go:build linux && native && !cgo

package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/didou07/dcc-lite/src/logx"
)

const nativeBuild = true

var nativeLog = logx.For("native")

var (
	nativeMu   sync.Mutex
	nativeProc *os.Process
	nativeStop = make(chan struct{})
	nativeOnce sync.Once
)

var chromiumNames = []string{
	"google-chrome-stable", "google-chrome", "chromium", "chromium-browser",
	"microsoft-edge-stable", "microsoft-edge", "brave-browser", "vivaldi", "vivaldi-stable", "opera",
}

func findAppBrowser() string {
	if v := strings.TrimSpace(os.Getenv("DCC_BROWSER")); v != "" {
		if p, err := exec.LookPath(v); err == nil {
			return p
		}
		nativeLog.Warn("DCC_BROWSER was not found", "value", v)
	}
	for _, n := range chromiumNames {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	return ""
}

func nativeClose() {
	nativeMu.Lock()
	p := nativeProc
	nativeMu.Unlock()
	if p != nil {
		_ = p.Signal(syscall.SIGTERM)
	}
	nativeOnce.Do(func() { close(nativeStop) })
}

// nativeNotice tells the user something when the program has no console (best effort).
func nativeNotice(text string) {
	nativeLog.Info(text)
	if p, err := exec.LookPath("notify-send"); err == nil {
		_ = exec.Command(p, "DCC Lite", text).Run()
	}
}

// runNative shows the window and returns when it is closed.
func runNative(d nativeDeps) error {
	bin := findAppBrowser()
	if bin == "" {
		nativeLog.Warn("no Chromium-based browser found for the app window; opening the interface in the default browser", "url", d.URL)
		return waitInDefaultBrowser(d)
	}
	profile := filepath.Join(d.DataDir, "webview-linux")
	if err := os.MkdirAll(profile, 0o700); err != nil {
		nativeLog.Warn("could not create the app window profile folder", "error", err)
		return waitInDefaultBrowser(d)
	}
	args := []string{
		"--app=" + d.URL,
		"--user-data-dir=" + profile,
		"--class=DCCLite",
		"--window-size=1280,820",
		"--no-first-run", "--no-default-browser-check", "--disable-features=Translate",
		"--ozone-platform-hint=auto",
	}
	args = append(args, strings.Fields(os.Getenv("DCC_BROWSER_ARGS"))...)
	cmd := exec.Command(bin, args...)
	started := time.Now()
	if err := cmd.Start(); err != nil {
		nativeLog.Warn("the app window could not be started", "browser", bin, "error", err)
		return waitInDefaultBrowser(d)
	}
	nativeMu.Lock()
	nativeProc = cmd.Process
	nativeMu.Unlock()
	nativeLog.Info("app window started", "browser", bin, "profile", profile)
	err := cmd.Wait()
	if time.Since(started) < 3*time.Second {
		select {
		case <-nativeStop: // we asked it to close
			return nil
		default:
		}
		nativeLog.Warn("the app window closed immediately; opening the interface in the default browser", "error", fmt.Sprint(err))
		return waitInDefaultBrowser(d)
	}
	return nil
}

func waitInDefaultBrowser(d nativeDeps) error {
	openBrowser(d.URL)
	nativeNotice("DCC Lite is running at " + d.URL + " (stop it with Ctrl+C in its terminal).")
	<-nativeStop
	return nil
}
