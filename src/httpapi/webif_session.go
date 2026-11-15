package httpapi

// Older Dreambox WebInterface (DreamOS) only. Nothing in this file runs for a receiver the owner set
// to OpenWebif, and OpenWebif's own code paths (openwebif.go) are not touched by it.

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

// ---- DreamOS / WebInterface "extended security" ----
//
// The Dreambox WebInterface plugin can require a session id on its /web/ addresses. Without one the
// receiver answers HTTP 412 "Precondition failed! sessionid is missing, invalid or expired!". The id is
// handed out by /web/session (which needs none) and is passed as the sessionid= argument.

type sessionEntry struct {
	id string
	at time.Time
}

var webSessions sync.Map // device base -> sessionEntry

var sessionIDPattern = regexp.MustCompile(`(?is)sessionid[^>]*>\s*([^<\s]+)\s*<`)
var sessionArgPattern = regexp.MustCompile(`([?&])sessionid=[^&]*&?`)

func (d deviceWeb) wantsSession(pathAndQuery string) bool {
	p := pathOnly(pathAndQuery)
	switch {
	case d.kind == "openwebif" || p == "/web/session":
		return false
	case strings.HasPrefix(p, "/web/"):
		return true
	case d.kind == "webinterface":
		return strings.HasPrefix(p, "/grab") || strings.HasPrefix(p, "/opkg") || strings.HasPrefix(p, "/ipkg")
	}
	return false
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

// get asks the receiver for an address. When the receiver answers 412 (session id missing or expired)
// a session id is fetched and the request is made once more.
func (d deviceWeb) get(ctx context.Context, pathAndQuery string) (*http.Response, error) {
	// OpenWebif receivers never reach this code. With the kind left on automatic only the old /web/
	// addresses (which OpenWebif does not use) may carry a session id; the owner choosing WebInterface
	// also allows it on /grab.
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
