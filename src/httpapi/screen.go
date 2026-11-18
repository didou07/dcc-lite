package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// freezeFrameSupported reports whether the Freeze Frame page offers this width.
func freezeFrameSupported(width int) bool {
	return width == 720 || width == 1280 || width == 1920 || width == 3840
}

func dreamOSResolution(width int) string {
	switch width {
	case 720:
		return "720x576"
	case 1280:
		return "1280x720"
	case 1920:
		return "1920x1080"
	}
	height := (width * 9 / 16) / 2 * 2
	return fmt.Sprintf("%dx%d", width, height)
}

func dreamOSFreezeFramePath(mode string, width int) (string, bool) {
	if !freezeFrameSupported(width) {
		return "", false
	}
	method := "auto"
	if mode == "all" {
		method = "ffmpeg"
	}
	return fmt.Sprintf("/freezeframe?screenshot=%s&resolution=%s&format=png&method=%s&refresh=&grab=Download", mode, dreamOSResolution(width), method), true
}

// dreamOSScreenshotPath is the receiver's own /screenshot address, used for the menus only.
func dreamOSScreenshotPath(mode string, width int) string {
	format := "format=jpg&jpgquali=90"
	if mode == "osd" {
		format = "format=png"
	}
	what := "osd=&video="
	switch mode {
	case "video":
		what = "video="
	case "osd":
		what = "osd="
	}
	return "/screenshot?" + format + "&res=" + dreamOSResolution(width) + "&" + what
}

func dreamOSGrabPath(mode string, width int) string {
	format := "format=jpg&jpgquali=90"
	if mode == "osd" {
		format = "format=png"
	}
	what := ""
	switch mode {
	case "video":
		what = "&v="
	case "osd":
		what = "&o="
	}
	file := url.QueryEscape("/tmp/" + strconv.FormatInt(time.Now().UnixMilli(), 10))
	return "/grab?" + format + "&r=" + strconv.Itoa(width) + what + "&filename=" + file
}

func grabAddresses(mode string, width int, kind string) []string {
	if kind != "dreamos" {
		return []string{fmt.Sprintf("/grab?format=jpg&mode=%s&r=%d", mode, width)}
	}
	var out []string
	if p, ok := dreamOSFreezeFramePath(mode, width); ok {
		out = append(out, p)
	}
	out = append(out, dreamOSGrabPath(mode, width))
	if mode == "osd" {
		out = append(out, dreamOSScreenshotPath(mode, width))
	}
	return out
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
	memoFn := "screen " + mode + " " + strconv.Itoa(width)
	if web.kind == "dreamos" {
		// try the address that worked last time first; the others only if it stops answering
		order = make([]int, 0, len(all))
		if name := legacyRecall(web.base, memoFn); name != "" {
			for i, a := range all {
				if pathOnly(a) == name {
					order = append(order, i)
					break
				}
			}
		}
		for i := range all {
			if len(order) == 0 || order[0] != i {
				order = append(order, i)
			}
		}
	} else if found && idx < len(all) {
		order = []int{idx}
	}
	var lastMsg string
	for n, i := range order {
		// the old interface can leave a screenshot request hanging: give each address its own
		// time, so the next one still has a chance within the overall limit
		actx, acancel := ctx, context.CancelFunc(func() {})
		if web.kind == "dreamos" && n < len(order)-1 {
			actx, acancel = context.WithTimeout(ctx, 12*time.Second)
		}
		resp, err := web.get(actx, all[i])
		if err != nil {
			acancel()
			if web.kind == "dreamos" && n < len(order)-1 && ctx.Err() == nil && errors.Is(err, context.DeadlineExceeded) {
				devLog.Warn("screenshot address did not answer in time", "base", web.base, "path", pathOnly(all[i]))
				lastMsg = "The receiver did not answer the screenshot request in time."
				continue
			}
			msg := web.unreachable(err)
			if errors.Is(err, context.DeadlineExceeded) {
				st.fail(msg)
			}
			return nil, &grabError{"web_unreachable", msg}
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxScreenBytes+1))
		resp.Body.Close()
		acancel()
		missing := resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed
		switch {
		case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
			return nil, &grabError{"web_auth", "The receiver's web interface rejected the saved user name or password."}
		case missing:
			lastMsg = "This receiver has no screenshot address (grab)."
			if web.kind == "dreamos" {
				legacyForget(web.base, memoFn)
			}
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
			case http.StatusBadRequest, http.StatusPreconditionFailed, http.StatusUnsupportedMediaType, http.StatusNotImplemented, http.StatusInternalServerError:
				if web.kind == "dreamos" && n < len(order)-1 {
					continue // the request was refused, not crashed: another form of the address may be accepted
				}
			}
		case rerr != nil || len(body) > maxScreenBytes:
			lastMsg = "The receiver sent an unusable picture."
		case !isPicture(body):
			lastMsg = "The receiver did not return a picture."
			if web.kind == "dreamos" && n < len(order)-1 {
				continue
			}
		default:
			st.mu.Lock()
			st.found, st.idx = true, i
			st.mu.Unlock()
			if web.kind == "dreamos" {
				legacyRemember(web.base, memoFn, pathOnly(all[i]))
			}
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
