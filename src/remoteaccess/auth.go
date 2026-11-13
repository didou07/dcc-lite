package remoteaccess

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	CookieName         = "__Host-dcc_session"
	sessionIdleTimeout = 12 * time.Hour
	sessionMaxAge      = 7 * 24 * time.Hour
	maxSessions        = 32
	maxTrackedClients  = 4096
	freeAttempts       = 5
	baseLockout        = time.Minute
	maxLockout         = 15 * time.Minute
	failureDelay       = 400 * time.Millisecond
)

type ErrThrottled struct{ RetryAfter time.Duration }

func (e ErrThrottled) Error() string { return "too many failed sign-in attempts; try again later" }

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

type Auth struct {
	creds *Credentials
	now   func() time.Time
	sleep func(time.Duration)

	mu       sync.Mutex
	sessions map[[32]byte]*session
	tries    map[string]*attempts
}

func NewAuth(creds *Credentials) *Auth {
	return &Auth{creds: creds, now: time.Now, sleep: time.Sleep, sessions: make(map[[32]byte]*session), tries: make(map[string]*attempts)}
}

func (a *Auth) cookieName() string { return CookieName }

func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

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

	validPassword, credentialRevision := a.creds.verify(password)
	if !validPassword {
		a.mu.Lock()
		t = a.tries[key]
		if t == nil {
			if len(a.tries) >= maxTrackedClients {
				a.evictOldestAttemptLocked()
			}
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
	if !a.creds.revisionMatches(credentialRevision) {
		a.mu.Unlock()
		return ErrBadPassword
	}
	delete(a.tries, key)
	if len(a.sessions) >= maxSessions {
		a.evictOldestLocked()
	}
	a.sessions[sha256.Sum256(token)] = &session{created: now, seen: now}
	a.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name: a.cookieName(), Value: base64.RawURLEncoding.EncodeToString(token), Path: "/",
		HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionMaxAge / time.Second),
	})
	return nil
}

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

func (a *Auth) Logout(w http.ResponseWriter, r *http.Request) {
	if cookie, err := r.Cookie(a.cookieName()); err == nil {
		if token, err := base64.RawURLEncoding.DecodeString(cookie.Value); err == nil && len(token) == 32 {
			a.mu.Lock()
			delete(a.sessions, sha256.Sum256(token))
			a.mu.Unlock()
		}
	}
	http.SetCookie(w, &http.Cookie{Name: a.cookieName(), Value: "", Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

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

func (a *Auth) evictOldestAttemptLocked() {
	var oldestKey string
	var oldest time.Time
	found := false
	for key, entry := range a.tries {
		if !found || entry.last.Before(oldest) {
			oldestKey, oldest, found = key, entry.last, true
		}
	}
	if found {
		delete(a.tries, oldestKey)
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

func SameOrigin(r *http.Request) bool {
	if r == nil || r.Host == "" {
		return false
	}
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return strings.EqualFold(u.Host, r.Host)
}
