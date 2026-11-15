package httpapi

// Older WebInterface (DreamOS) only; never runs for receivers set to OpenWebif.

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

// DreamOS WebInterface "extended security": its /web/ addresses may need a sessionid (else HTTP 412),
// obtained from /web/session and passed as sessionid=.

type sessionEntry struct {
	id string
	at time.Time
}

var webSessions sync.Map // device base -> sessionEntry

var sessionIDPattern = regexp.MustCompile(`(?is)sessionid[^>]*>\s*([^<\s]+)\s*<`)
var sessionArgPattern = regexp.MustCompile(`([?&])sessionid=[^&]*&?`)

// wantsSession: only a receiver the owner set to WebInterface ever gets a session id.
func (d deviceWeb) wantsSession(pathAndQuery string) bool {
	if d.kind != "webinterface" {
		return false
	}
	p := pathOnly(pathAndQuery)
	switch {
	case p == "/web/session":
		return false
	case strings.HasPrefix(p, "/web/"):
		return true
	}
	return strings.HasPrefix(p, "/grab") || strings.HasPrefix(p, "/screenshot") ||
		strings.HasPrefix(p, "/opkg") || strings.HasPrefix(p, "/ipkg")
}

// webUnsupportedError: this receiver is set to WebInterface, which has no such feature.
type webUnsupportedError struct{ feature string }

func (e *webUnsupportedError) Error() string {
	return e.feature + " needs OpenWebif; this receiver is set to WebInterface"
}

// webifLacks names what the older WebInterface does not offer, or "" when the address is fine to ask for.
// Everything under /api/ is OpenWebif's; a WebInterface receiver is only asked for /web/ addresses
// (the /api/ ones that have a /web/ twin are translated in legacywebif.go before they get here).
func (d deviceWeb) webifLacks(pathAndQuery string) string {
	if d.kind != "webinterface" {
		return ""
	}
	p := pathOnly(pathAndQuery)
	switch {
	case strings.HasPrefix(p, "/api/"):
		return strings.TrimPrefix(p, "/api/")
	case strings.HasPrefix(p, "/autotimer"):
		return "AutoTimer"
	case strings.HasPrefix(p, "/epgrefresh"):
		return "EPG refresh"
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
	resp, err := d.getOnce(ctx, "/web/session")
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
		devLog.Info("receiver wants a session id (WebInterface extended security), using one", "base", d.base)
	}
	webSessions.Store(d.base, sessionEntry{id: id, at: time.Now()})
	return id, nil
}

// get requests an address; on HTTP 412 it fetches a session id and retries once.
func (d deviceWeb) get(ctx context.Context, pathAndQuery string) (*http.Response, error) {
	if what := d.webifLacks(pathAndQuery); what != "" {
		return nil, &webUnsupportedError{what}
	}
	// Only old /web/ addresses may carry a session id (and /grab when the owner chose WebInterface).
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
