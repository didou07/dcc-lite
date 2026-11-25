//go:build (windows && native) || (linux && native && cgo) || (darwin && native)

package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"

	"github.com/didou07/dcc-lite/src/vlc"
)

const maxOverlayBytes = 24 << 20

var overlay struct {
	mu          sync.Mutex
	flip        int
	unsupported atomic.Bool
	warned      atomic.Bool
}

func overlayPath() (string, error) {
	dir := filepath.Join(os.TempDir(), "dcclite-osd")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	overlay.mu.Lock()
	overlay.flip ^= 1
	name := "osd-a.png"
	if overlay.flip == 1 {
		name = "osd-b.png"
	}
	overlay.mu.Unlock()
	return filepath.Join(dir, name), nil
}

func (n *nativeLive) overlayRequest(raw string) (string, error) {
	var a struct {
		PNG   string `json:"png"`
		Clear bool   `json:"clear"`
	}
	if err := json.Unmarshal([]byte(raw), &a); err != nil {
		return "", err
	}
	reply := func() (string, error) {
		return jsonString(map[string]any{"ok": true, "unsupported": overlay.unsupported.Load()})
	}
	if a.Clear {
		n.withPlayer(func(p *vlc.Player) { p.ClearOverlay() })
		return reply()
	}
	body, err := base64.StdEncoding.DecodeString(a.PNG)
	if err != nil {
		return "", errors.New("the overlay picture is not valid base64")
	}
	if len(body) == 0 || len(body) > maxOverlayBytes || !bytes.HasPrefix(body, []byte("\x89PNG\r\n\x1a\n")) {
		return "", errors.New("the overlay must be a PNG picture")
	}
	path, err := overlayPath()
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		return "", err
	}
	n.withPlayer(func(p *vlc.Player) {
		if err := p.SetOverlay(path); err != nil {
			if !p.OverlaySupported() {
				overlay.unsupported.Store(true)
			}
			if overlay.warned.CompareAndSwap(false, true) {
				nativeLog.Warn("the OSD overlay could not be shown", "error", err)
			}
		}
	})
	return reply()
}
