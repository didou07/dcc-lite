package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

const (
	grabTimeout     = 25 * time.Second
	grabMinInterval = 400 * time.Millisecond
	grabFailPause   = 5 * time.Second
	grabFailPauseMx = 30 * time.Second
	grabGap         = 350 * time.Millisecond
)

type grabState struct {
	gate    chan struct{}
	mu      sync.Mutex
	lastAt  time.Time
	lastKey string
	last    []byte
	failAt  time.Time
	streak  int
	failMsg string
	doneAt  time.Time
}

var grabStates sync.Map

func grabStateFor(base string) *grabState {
	if v, ok := grabStates.Load(base); ok {
		return v.(*grabState)
	}
	v, _ := grabStates.LoadOrStore(base, &grabState{gate: make(chan struct{}, 1)})
	return v.(*grabState)
}

type grabError struct {
	code, msg string
}

func (g *grabState) coolingDown() (string, time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.streak == 0 {
		return "", 0
	}
	pause := time.Duration(g.streak) * grabFailPause
	if pause > grabFailPauseMx {
		pause = grabFailPauseMx
	}
	if left := pause - time.Since(g.failAt); left > 0 {
		return g.failMsg, left
	}
	return "", 0
}

func (g *grabState) fail(msg string) {
	g.mu.Lock()
	g.streak++
	g.failAt = time.Now()
	g.failMsg = msg
	g.mu.Unlock()
}

func (g *grabState) ok(key string, body []byte) {
	g.mu.Lock()
	g.streak = 0
	g.lastAt = time.Now()
	g.lastKey = key
	g.last = body
	g.mu.Unlock()
}

func (g *grabState) cached(key string) []byte {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.last != nil && g.lastKey == key && time.Since(g.lastAt) < grabMinInterval {
		return g.last
	}
	return nil
}

func fetchGrab(ctx context.Context, web deviceWeb, mode string, width int) ([]byte, *grabError) {
	st := grabStateFor(web.base)
	key := fmt.Sprintf("%s/%d", mode, width)
	if msg, left := st.coolingDown(); left > 0 {
		return nil, &grabError{"web_error", fmt.Sprintf("%s Pausing screen requests for %d s so the receiver can recover.", msg, int(left.Seconds())+1)}
	}
	select {
	case st.gate <- struct{}{}:
	case <-ctx.Done():
		return nil, &grabError{"web_error", "cancelled"}
	}
	defer func() {
		st.mu.Lock()
		st.doneAt = time.Now()
		st.mu.Unlock()
		<-st.gate
	}()
	st.mu.Lock()
	wait := grabGap - time.Since(st.doneAt)
	st.mu.Unlock()
	if wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return nil, &grabError{"web_error", "cancelled"}
		}
	}
	if body := st.cached(key); body != nil {
		return body, nil
	}
	if msg, left := st.coolingDown(); left > 0 {
		return nil, &grabError{"web_error", fmt.Sprintf("%s Pausing screen requests for %d s so the receiver can recover.", msg, int(left.Seconds())+1)}
	}
	upstream, cancel := context.WithTimeout(context.WithoutCancel(ctx), grabTimeout)
	defer cancel()
	resp, err := web.get(upstream, fmt.Sprintf("/grab?format=jpg&mode=%s&r=%d", mode, width))
	if err != nil {
		code := "web_unreachable"
		msg := web.unreachable(err)
		if errors.Is(err, context.DeadlineExceeded) {
			st.fail(msg)
		}
		return nil, &grabError{code, msg}
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, &grabError{"web_auth", "The receiver's web interface rejected the saved user name or password."}
	}
	if resp.StatusCode != http.StatusOK {
		msg := fmt.Sprintf("The receiver's screenshot tool (grab) failed (HTTP %d). On some images this can crash Enigma2.", resp.StatusCode)
		st.fail(msg)
		return nil, &grabError{"web_error", msg}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxScreenBytes+1))
	if err != nil || len(body) > maxScreenBytes {
		msg := "The receiver sent an unusable picture."
		st.fail(msg)
		return nil, &grabError{"web_error", msg}
	}
	if len(body) < 3 || body[0] != 0xFF || body[1] != 0xD8 || body[2] != 0xFF {
		msg := "The receiver did not return a picture (is the grab feature available on this image?)."
		st.fail(msg)
		return nil, &grabError{"web_error", msg}
	}
	st.ok(key, body)
	return body, nil
}

const maxScreenBytes = 16 << 20

var screenWidths = map[int]bool{320: true, 480: true, 640: true, 720: true, 960: true, 1280: true, 1920: true, 3840: true}

func (s *Server) deviceScreen(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.app.Profiles.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "device profile not found")
		return
	}
	if !profile.Enabled {
		writeError(w, http.StatusConflict, "device profile is disabled")
		return
	}
	mode := r.URL.Query().Get("mode")
	switch mode {
	case "all", "video", "osd":
	case "":
		mode = "all"
	default:
		writeError(w, http.StatusBadRequest, "mode must be all, video or osd")
		return
	}
	width := 720
	if raw := r.URL.Query().Get("width"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || !screenWidths[n] {
			writeError(w, http.StatusBadRequest, "width must be 320, 480, 640, 720, 960, 1280, 1920 or 3840")
			return
		}
		width = n
	}
	web := newDeviceWeb(profile, grabTimeout)
	body, gerr := fetchGrab(r.Context(), web, mode, width)
	if gerr != nil {
		if r.Context().Err() != nil {
			return
		}
		status := http.StatusBadGateway
		writeJSON(w, status, map[string]any{"error": gerr.msg, "code": gerr.code})
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}
