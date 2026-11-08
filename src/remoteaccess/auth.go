package remoteaccess

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	// CookieName uses the __Host- prefix: the browser only accepts it over HTTPS,
	// with Path=/ and no Domain, so it cannot be set by a sibling host.
	CookieName = "__Host-dcc_session"
	// PlainCookieName is used when the listener serves plain HTTP (opt-in, not
	// encrypted): browsers refuse __Host- and Secure cookies over HTTP.
	PlainCookieName = "dcc_session"

	// A sign-in stays valid while the page keeps talking to the server (the page
	// sends a heartbeat and the terminal WebSocket refreshes it), so an open
	// terminal or file transfer is not cut off by the session timing out.
	sessionIdleTimeout = 12 * time.Hour
	sessionMaxAge      = 7 * 24 * time.Hour
	maxSessions        = 32
	freeAttempts       = 5
	baseLockout        = time.Minute
	maxLockout         = 15 * time.Minute
	failureDelay       = 400 * time.Millisecond
)

// ErrThrottled is returned when an address has too many recent failed logins.
type ErrThrottled struct{ RetryAfter time.Duration }

func (e ErrThrottled) Error() string { return "too many failed sign-in attempts; try again later" }

// ErrBadPassword is returned for a wrong password.
var ErrBadPassword = errors.New("incorrect password")

type session struct {
	created time.Time
	seen    time.Time
}

type attempts struct {
	failures int
	locked   time.Time
	last     time.Time
}

// Auth owns login sessions. Tokens are random, only their SHA-256 is kept in memory.
type Auth struct {
	creds *Credentials
	plain bool // plain HTTP listener: no Secure cookie, no __Host- prefix
	now   func() time.Time
	sleep func(time.Duration)

	mu       sync.Mutex
	sessions map[[32]byte]*session
	tries    map[string]*attempts
}

// NewAuth builds an authenticator over a stored password.
func NewAuth(creds *Credentials) *Auth {
	return &Auth{creds: creds, now: time.Now, sleep: time.Sleep, sessions: make(map[[32]byte]*session), tries: make(map[string]*attempts)}
}

// NewPlainAuth is NewAuth for a listener that serves plain HTTP. The session
// cookie then cannot be Secure, so everything (password included) travels
// unencrypted; it exists for people who reach the program through a public IP or
// a dynamic DNS name without a certificate.
func NewPlainAuth(creds *Credentials) *Auth {
	a := NewAuth(creds)
	a.plain = true
	return a
}

// Plain reports whether the authenticator belongs to a plain HTTP listener.
func (a *Auth) Plain() bool { return a.plain }

func (a *Auth) cookieName() string {
	if a.plain {
		return PlainCookieName
	}
	return CookieName
}

// clientKey identifies the remote peer by IP address (port stripped).
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Login verifies the password and, on success, sets the session cookie.
func (a *Auth) Login(w http.ResponseWriter, r *http.Request, password string) error {
	key := clientKey(r)
	now := a.now()
	a.mu.Lock()
	a.pruneLocked(now)
	t := a.tries[key]
	if t != nil && now.Before(t.locked) {
		retry := t.locked.Sub(now)
		a.mu.Unlock()
		return ErrThrottled{RetryAfter: retry}
	}
	a.mu.Unlock()

	if !a.creds.Verify(password) {
		a.mu.Lock()
		t = a.tries[key]
		if t == nil {
			t = &attempts{}
			a.tries[key] = t
		}
		t.failures++
		t.last = now
		if t.failures >= freeAttempts {
			lock := baseLockout << uint(min(t.failures-freeAttempts, 4))
			if lock > maxLockout {
				lock = maxLockout
			}
			t.locked = now.Add(lock)
		}
		a.mu.Unlock()
		a.sleep(failureDelay)
		return ErrBadPassword
	}

	token := make([]byte, 32)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	a.mu.Lock()
	delete(a.tries, key)
	if len(a.sessions) >= maxSessions {
		a.evictOldestLocked()
	}
	a.sessions[sha256.Sum256(token)] = &session{created: now, seen: now}
	a.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name: a.cookieName(), Value: base64.RawURLEncoding.EncodeToString(token), Path: "/",
		HttpOnly: true, Secure: !a.plain, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionMaxAge / time.Second),
	})
	return nil
}

// Authenticated reports whether the request carries a live session, and
// extends its idle timeout.
func (a *Auth) Authenticated(r *http.Request) bool {
	cookie, err := r.Cookie(a.cookieName())
	if err != nil {
		return false
	}
	token, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(token) != 32 {
		return false
	}
	id := sha256.Sum256(token)
	now := a.now()
	a.mu.Lock()
	defer a.mu.Unlock()
	s := a.sessions[id]
	if s == nil {
		return false
	}
	if now.Sub(s.seen) > sessionIdleTimeout || now.Sub(s.created) > sessionMaxAge {
		delete(a.sessions, id)
		return false
	}
	s.seen = now
	return true
}

// Logout ends the session of the request and clears the cookie.
func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(a.cookieName()); err == nil {
		if token, err := base64.RawURLEncoding.DecodeString(cookie.Value); err == nil && len(token) == 32 {
			a.mu.Lock()
			delete(a.sessions, sha256.Sum256(token))
			a.mu.Unlock()
		}
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: "", Path: "/", HttpOnly: true, Secure: !a.plain, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

// RevokeAll drops every session (used when the password changes).
func (a *Auth) RevokeAll() {
	a.mu.Lock()
	a.sessions = make(map[[32]byte]*session)
	a.mu.Unlock()
}

func (a *Auth) pruneLocked(now time.Time) {
	for key, t := range a.tries {
		if now.After(t.locked) && now.Sub(t.last) > 30*time.Minute {
			delete(a.tries, key)
		}
	}
	for id, s := range a.sessions {
		if now.Sub(s.seen) > sessionIdleTimeout || now.Sub(s.created) > sessionMaxAge {
			delete(a.sessions, id)
		}
	}
}

func (a *Auth) evictOldestLocked() {
	var oldestID [32]byte
	var oldest time.Time
	found := false
	for id, s := range a.sessions {
		if !found || s.seen.Before(oldest) {
			oldestID, oldest, found = id, s.seen, true
		}
	}
	if found {
		delete(a.sessions, oldestID)
	}
}

// SameOrigin reports whether an Origin header names the same host the request
// was sent to. A missing Origin is not a match.
func SameOrigin(r *http.Request) bool { return SameOriginFor(r, true) }

// SameOriginFor is SameOrigin for an https:// (secure) or http:// page.
func SameOriginFor(r *http.Request, secure bool) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || r.Host == "" {
		return false
	}
	scheme := "https://"
	if !secure {
		scheme = "http://"
	}
	if !strings.HasPrefix(strings.ToLower(origin), scheme) {
		return false
	}
	rest := origin[len(scheme):]
	if strings.ContainsAny(rest, "/?#@") {
		return false
	}
	if strings.EqualFold(rest, r.Host) {
		return true
	}
	// Behind a reverse proxy or port forward the Host header may be rewritten to
	// the internal address. A page on another site cannot set X-Forwarded-Host
	// (a custom header needs a preflight this server never approves), so the
	// forwarded name is as trustworthy as Host itself.
	if forwarded := strings.TrimSpace(strings.Split(r.Header.Get("X-Forwarded-Host"), ",")[0]); forwarded != "" {
		return strings.EqualFold(rest, forwarded)
	}
	return false
}
