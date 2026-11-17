package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
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
	// the address that last gave a picture, so a receiver is probed once and not on every refresh
	found bool
	idx   int
	kind  string
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
		return nil, &grabError{"web_error", fmt.Sprintf("%s Pausing for %d s.", msg, int(left.Seconds())+1)}
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
		return nil, &grabError{"web_error", fmt.Sprintf("%s Pausing for %d s.", msg, int(left.Seconds())+1)}
	}
	upstream, cancel := context.WithTimeout(context.WithoutCancel(ctx), grabTimeout)
	defer cancel()
	body, gerr := grabOnce(upstream, web, st, mode, width)
	if gerr != nil {
		return nil, gerr
	}
	st.ok(key, body)
	return body, nil
}

const maxScreenBytes = 16 << 20

func dreamOSScreenSupported(mode string, width int) bool {
	switch mode {
	case "all", "video":
		return width == 1280
	case "osd":
		return width == 720 || width == 1280 || width == 1920
	default:
		return false
	}
}

func dreamOSResolution(width int) (string, bool) {
	switch width {
	case 720:
		return "720x576", true
	case 1280:
		return "1280x720", true
	case 1920:
		return "1920x1080", true
	default:
		return "", false
	}
}

func dreamOSFreezeFramePath(mode string, width int) (string, bool) {
	if !dreamOSScreenSupported(mode, width) {
		return "", false
	}
	resolution, ok := dreamOSResolution(width)
	if !ok {
		return "", false
	}
	method := "auto"
	if mode == "all" {
		method = "ffmpeg"
	}
	return fmt.Sprintf("/freezeframe?screenshot=%s&resolution=%s&format=png&method=%s&refresh=&grab=Download", mode, resolution, method), true
}

func grabAddresses(mode string, width int, kind string) []string {
	if kind != "dreamos" {
		return []string{fmt.Sprintf("/grab?format=jpg&mode=%s&r=%d", mode, width)}
	}
	filename := url.QueryEscape("/tmp/" + strconv.FormatInt(time.Now().UnixMilli(), 10))
	freezeFrame, _ := dreamOSFreezeFramePath(mode, width)
	return []string{
		freezeFrame,
		"/screenshot?format=png&filename=" + filename,
		"/screenshot?display=",
		"/grab?format=jpg&filename=" + filename,
	}
}

func isPNG(body []byte) bool {
	return len(body) >= 8 && string(body[:8]) == "\x89PNG\r\n\x1a\n"
}

func isPicture(body []byte) bool {
	if len(body) >= 3 && body[0] == 0xFF && body[1] == 0xD8 && body[2] == 0xFF {
		return true
	}
	return isPNG(body)
}

func grabOnce(ctx context.Context, web deviceWeb, st *grabState, mode string, width int) ([]byte, *grabError) {
	all := grabAddresses(mode, width, web.kind)
	st.mu.Lock()
	if st.kind != web.kind {
		st.kind, st.found = web.kind, false
	}
	idx, found := st.idx, st.found
	st.mu.Unlock()
	order := []int{0}
	if web.kind == "dreamos" {
		order = make([]int, len(all))
		for i := range order {
			order[i] = i
		}
	} else if found && idx < len(all) {
		order = []int{idx}
	}
	var lastMsg string
	for n, i := range order {
		resp, err := web.get(ctx, all[i])
		if err != nil {
			msg := web.unreachable(err)
			if errors.Is(err, context.DeadlineExceeded) {
				st.fail(msg)
			}
			return nil, &grabError{"web_unreachable", msg}
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxScreenBytes+1))
		resp.Body.Close()
		missing := resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, &grabError{"web_auth", "The receiver's web interface rejected the saved user name or password."}
		case missing:
			lastMsg = "This receiver has no screenshot address (grab)."
			if found {
				st.mu.Lock()
				st.found = false
				st.mu.Unlock()
			}
			continue // only a missing address moves on to the next one
		case resp.StatusCode != http.StatusOK:
			lastMsg = fmt.Sprintf("The receiver's screenshot address failed (HTTP %d).", resp.StatusCode)
			devLog.Warn("screenshot address refused", "base", web.base, "path", pathOnly(all[i]), "status", resp.StatusCode)
			switch resp.StatusCode {
			case http.StatusBadRequest, http.StatusPreconditionFailed, http.StatusUnsupportedMediaType, http.StatusNotImplemented:
				if web.kind == "dreamos" && n < len(order)-1 {
					continue // the request was refused, not crashed: another form of the address may be accepted
				}
			}
		case rerr != nil || len(body) > maxScreenBytes:
			lastMsg = "The receiver sent an unusable picture."
		case !isPicture(body):
			lastMsg = "The receiver did not return a picture."
			if web.kind == "dreamos" && !strings.HasPrefix(all[i], "/freezeframe?") && n < len(order)-1 {
				continue
			}
		default:
			st.mu.Lock()
			st.found, st.idx = true, i
			st.mu.Unlock()
			return body, nil
		}
		break // a real failure: stop, do not ask again
	}
	st.fail(lastMsg)
	return nil, &grabError{"web_error", lastMsg}
}

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
	if web.kind == "dreamos" && !dreamOSScreenSupported(mode, width) {
		writeError(w, http.StatusBadRequest, "Verified DreamOS options: Video or All at 1280 px; OSD at 720, 1280 or 1920 px.")
		return
	}
	body, gerr := fetchGrab(r.Context(), web, mode, width)
	if gerr != nil {
		if r.Context().Err() != nil {
			return
		}
		status := http.StatusBadGateway
		writeJSON(w, status, map[string]any{"error": gerr.msg, "code": gerr.code})
		return
	}
	ctype := "image/jpeg"
	if isPNG(body) {
		ctype = "image/png"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}
