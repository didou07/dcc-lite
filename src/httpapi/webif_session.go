package httpapi


import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"
)

type sessionEntry struct {
	id string
	at time.Time
}

var webSessions sync.Map // device base -> sessionEntry

var sessionIDPattern = regexp.MustCompile(`(?is)sessionid[^>]*>\s*([^<\s]+)\s*<`)
var sessionArgPattern = regexp.MustCompile(`([?&])sessionid=[^&]*&?`)

func (d deviceWeb) wantsSession(pathAndQuery string) bool {
	if d.kind != "dreamos" {
		return false
	}
	p := pathOnly(pathAndQuery)
	switch {
	case p == "/web/session", p == "/web/strings.js", strings.HasSuffix(p, ".m3u"):
		return false
	case strings.HasPrefix(p, "/grab"), strings.HasPrefix(p, "/screenshot"),
		strings.HasPrefix(p, "/public"), strings.HasPrefix(p, "/api/"):
		return false
	}
	return true
}

type webUnsupportedError struct{ feature string }

func (e *webUnsupportedError) Error() string {
	return e.feature + " needs OpenWebif; this receiver is set to DreamOS"
}

func (d deviceWeb) webifLacks(pathAndQuery string) string {
	if d.kind != "dreamos" {
		return ""
	}
	p := pathOnly(pathAndQuery)
	switch {
	case strings.HasPrefix(p, "/api/"):
		return strings.TrimPrefix(p, "/api/")
	}
	return ""
}

func withSessionID(pathAndQuery, id string) string {
	clean := strings.TrimRight(sessionArgPattern.ReplaceAllString(pathAndQuery, "$1"), "?&")
	sep := "?"
	if strings.Contains(clean, "?") {
		sep = "&"
	}
	return clean + sep + "sessionid=" + url.QueryEscape(id)
}

// fetchSession asks the receiver for a session id and remembers it.
func (d deviceWeb) fetchSession(ctx context.Context) (string, error) {
	resp, err := d.sendOnce(ctx, http.MethodPost, "/web/session", "sessionid=0")
	if err != nil {
		return "", err
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from /web/session", resp.StatusCode)
	}
	id := ""
	if m := sessionIDPattern.FindSubmatch(body); m != nil {
		id = string(m[1])
	} else if t := strings.TrimSpace(string(body)); t != "" && !strings.ContainsAny(t, "<>") && len(t) <= 128 {
		id = t
	}
	if id == "" {
		return "", errors.New("/web/session gave no session id")
	}
	if _, known := webSessions.Load(d.base); !known {
		devLog.Info("receiver wants a session id (DreamOS extended security), using one", "base", d.base)
	}
	webSessions.Store(d.base, sessionEntry{id: id, at: time.Now()})
	return id, nil
}

var webPostPaths sync.Map // device base + "|" + path -> true

func (d deviceWeb) canPost(pathAndQuery string) bool {
	if d.kind != "dreamos" {
		return false
	}
	p := pathOnly(pathAndQuery)
	if p == "/web/session" {
		return false
	}
	return (strings.HasPrefix(p, "/web/") && p != "/web/strings.js" && !strings.HasSuffix(p, ".m3u")) ||
		p == "/autotimer" || strings.HasPrefix(p, "/autotimer/")
}

func (d deviceWeb) post(ctx context.Context, pathAndQuery string) (*http.Response, error) {
	path, rawQuery, _ := strings.Cut(pathAndQuery, "?")
	args, _ := url.ParseQuery(sessionArgPattern.ReplaceAllString(rawQuery, "$1"))
	args.Del("sessionid")
	send := func() (*http.Response, error) {
		form := url.Values{}
		for k, v := range args {
			form[k] = v
		}
		if v, ok := webSessions.Load(d.base); ok {
			form.Set("sessionid", v.(sessionEntry).id)
		}
		return d.sendOnce(ctx, http.MethodPost, path, form.Encode())
	}
	resp, err := send()
	if err != nil || resp.StatusCode != http.StatusPreconditionFailed {
		return resp, err
	}
	_ = resp.Body.Close()
	if _, err = d.fetchSession(ctx); err != nil {
		devLog.Warn("could not get a session id from the receiver", "base", d.base, "error", err)
	}
	return send()
}

func (d deviceWeb) get(ctx context.Context, pathAndQuery string) (*http.Response, error) {
	if what := d.webifLacks(pathAndQuery); what != "" {
		return nil, &webUnsupportedError{what}
	}
	if !d.canPost(pathAndQuery) {
		return d.getSession(ctx, pathAndQuery)
	}
	key := d.base + "|" + pathOnly(pathAndQuery)
	if _, known := webPostPaths.Load(key); known {
		return d.post(ctx, pathAndQuery)
	}
	resp, err := d.post(ctx, pathAndQuery)
	if err != nil || resp.StatusCode != http.StatusMethodNotAllowed {
		if err == nil {
			webPostPaths.LoadOrStore(key, true)
		}
		return resp, err
	}
	_ = resp.Body.Close()
	return d.getSession(ctx, pathAndQuery)
}

func (d deviceWeb) getSession(ctx context.Context, pathAndQuery string) (*http.Response, error) {
	if !d.wantsSession(pathAndQuery) {
		return d.getOnce(ctx, pathAndQuery)
	}
	base := sessionArgPattern.ReplaceAllString(pathAndQuery, "$1")
	req := pathAndQuery
	if v, ok := webSessions.Load(d.base); ok {
		req = withSessionID(base, v.(sessionEntry).id)
	}
	resp, err := d.getOnce(ctx, req)
	if err != nil || resp.StatusCode != http.StatusPreconditionFailed {
		return resp, err
	}
	_ = resp.Body.Close()
	id, serr := d.fetchSession(ctx)
	if serr != nil {
		devLog.Warn("could not get a session id from the receiver", "base", d.base, "error", serr)
		return d.getOnce(ctx, base) // hand back the receiver's own answer
	}
	return d.getOnce(ctx, withSessionID(base, id))
}

func (d deviceWeb) fileAddress(file string) string {
	if d.kind == "dreamos" {
		return "/file?file=" + url.QueryEscape(file)
	}
	return "/file?action=download&file=" + url.QueryEscape(file)
}
