package remoteaccess

import (
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestCredentials(t *testing.T, password string) *Credentials {
	t.Helper()
	c, err := OpenCredentials(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Set(password); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCredentialsHashVerifyAndPersist(t *testing.T) {
	dir := t.TempDir()
	c, err := OpenCredentials(dir)
	if err != nil {
		t.Fatal(err)
	}
	if c.Configured() || c.Verify("anything-long-enough") {
		t.Fatal("empty store must not verify")
	}
	if err := c.Set("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := c.Set("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "remote.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "correct horse") {
		t.Fatal("password stored in clear text")
	}
	if info, _ := os.Stat(filepath.Join(dir, "remote.json")); info != nil && info.Mode().Perm()&0o077 != 0 && os.PathSeparator == '/' {
		t.Fatalf("remote.json permissions %v are too open", info.Mode().Perm())
	}
	reopened, err := OpenCredentials(dir)
	if err != nil || !reopened.Configured() {
		t.Fatalf("reopen: %v", err)
	}
	if !reopened.Verify("correct horse battery") || reopened.Verify("correct horse batterz") || reopened.Verify("") {
		t.Fatal("verification result wrong after reopen")
	}
}

func TestGeneratedPasswordIsStrongAndWorks(t *testing.T) {
	c, _ := OpenCredentials(t.TempDir())
	first, err := c.Generate()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := c.Generate()
	if first == second {
		t.Fatal("generated passwords repeat")
	}
	if len(strings.ReplaceAll(first, "-", "")) != 20 || !c.Verify(second) || c.Verify(first) {
		t.Fatalf("generated password %q unusable or old one still valid", first)
	}
}

func TestOpenCredentialsRejectsWeakFile(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "remote.json"), []byte(`{"version":1,"salt":"AAAA","hash":"AAAA","iterations":10}`), 0o600)
	if _, err := OpenCredentials(dir); err == nil {
		t.Fatal("weak iteration count accepted")
	}
}

func loginRequest(addr string) (*httptest.ResponseRecorder, *http.Request) {
	r := httptest.NewRequest(http.MethodPost, "https://box.local:8788/api/v1/auth/login", nil)
	r.RemoteAddr = addr
	return httptest.NewRecorder(), r
}

func TestLoginSessionLifecycle(t *testing.T) {
	auth := NewAuth(newTestCredentials(t, "correct horse battery"))
	auth.sleep = func(time.Duration) {}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }

	w, r := loginRequest("192.168.1.20:5000")
	if err := auth.Login(w, r, "wrong password"); err != ErrBadPassword {
		t.Fatalf("wrong password error = %v", err)
	}
	w, r = loginRequest("192.168.1.20:5000")
	if err := auth.Login(w, r, "correct horse battery"); err != nil {
		t.Fatalf("login: %v", err)
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != CookieName || !cookies[0].HttpOnly || !cookies[0].Secure || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatalf("session cookie flags wrong: %#v", cookies)
	}
	authed := httptest.NewRequest(http.MethodGet, "https://box.local:8788/", nil)
	authed.AddCookie(cookies[0])
	if !auth.Authenticated(authed) {
		t.Fatal("fresh session rejected")
	}
	if auth.Authenticated(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Fatal("request without cookie accepted")
	}
	forged := httptest.NewRequest(http.MethodGet, "/", nil)
	forged.AddCookie(&http.Cookie{Name: CookieName, Value: strings.Repeat("A", 43)})
	if auth.Authenticated(forged) {
		t.Fatal("forged cookie accepted")
	}

	now = now.Add(sessionIdleTimeout + time.Minute)
	if auth.Authenticated(authed) {
		t.Fatal("idle session did not expire")
	}

	w, r = loginRequest("192.168.1.20:5001")
	if err := auth.Login(w, r, "correct horse battery"); err != nil {
		t.Fatal(err)
	}
	again := httptest.NewRequest(http.MethodGet, "/", nil)
	again.AddCookie(w.Result().Cookies()[0])
	out := httptest.NewRecorder()
	auth.Logout(out, again)
	if auth.Authenticated(again) {
		t.Fatal("session survived logout")
	}
}

func TestLoginThrottlesRepeatedFailuresPerAddress(t *testing.T) {
	auth := NewAuth(newTestCredentials(t, "correct horse battery"))
	auth.sleep = func(time.Duration) {}
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	auth.now = func() time.Time { return now }
	for i := 0; i < freeAttempts; i++ {
		w, r := loginRequest("10.0.0.9:1000")
		if err := auth.Login(w, r, "nope nope nope"); err != ErrBadPassword {
			t.Fatalf("attempt %d error = %v", i, err)
		}
	}
	w, r := loginRequest("10.0.0.9:2000")
	if _, ok := auth.Login(w, r, "correct horse battery").(ErrThrottled); !ok {
		t.Fatal("locked address could still log in with the right password")
	}
	w, r = loginRequest("10.0.0.10:2000")
	if err := auth.Login(w, r, "correct horse battery"); err != nil {
		t.Fatalf("other address must not be locked: %v", err)
	}
	now = now.Add(baseLockout + time.Second)
	w, r = loginRequest("10.0.0.9:3000")
	if err := auth.Login(w, r, "correct horse battery"); err != nil {
		t.Fatalf("lockout did not expire: %v", err)
	}
}

func TestSameOrigin(t *testing.T) {
	cases := []struct {
		origin, host string
		want         bool
	}{
		{"https://box.local:8788", "box.local:8788", true},
		{"https://BOX.local:8788", "box.local:8788", true},
		{"", "box.local:8788", false},
		{"http://box.local:8788", "box.local:8788", false},
		{"https://evil.example", "box.local:8788", false},
		{"https://box.local:8788/path", "box.local:8788", false},
		{"https://user@box.local:8788", "box.local:8788", false},
		{"null", "box.local:8788", false},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodPost, "https://"+c.host+"/", nil)
		r.Host = c.host
		if c.origin != "" {
			r.Header.Set("Origin", c.origin)
		}
		if got := SameOrigin(r); got != c.want {
			t.Errorf("SameOrigin(origin=%q host=%q) = %v, want %v", c.origin, c.host, got, c.want)
		}
	}
}

func TestSameOriginBehindProxy(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "https://10.0.0.5:8788/", nil)
	r.Host = "10.0.0.5:8788"
	r.Header.Set("Origin", "https://105.105.220.122")
	if SameOrigin(r) {
		t.Fatal("origin accepted without a forwarded host")
	}
	r.Header.Set("X-Forwarded-Host", "105.105.220.122")
	if !SameOrigin(r) {
		t.Fatal("origin rejected although it matches X-Forwarded-Host")
	}
	r.Header.Set("Origin", "https://evil.example")
	if SameOrigin(r) {
		t.Fatal("foreign origin accepted")
	}
}

func TestSelfSignedCertificateCreatedReusedAndRenewedForNewHosts(t *testing.T) {
	dir := t.TempDir()
	first, err := LoadOrCreateSelfSigned(dir, []string{"localhost", "127.0.0.1", "192.168.1.5"})
	if err != nil {
		t.Fatal(err)
	}
	if first.Source != "self-signed" || strings.Count(first.Fingerprint, ":") != 31 {
		t.Fatalf("unexpected certificate: %#v", first)
	}
	if info, _ := os.Stat(filepath.Join(dir, "tls", "key.pem")); info != nil && os.PathSeparator == '/' && info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("private key permissions %v are too open", info.Mode().Perm())
	}
	reused, err := LoadOrCreateSelfSigned(dir, []string{"192.168.1.5", "localhost"})
	if err != nil || reused.Fingerprint != first.Fingerprint {
		t.Fatalf("certificate was regenerated although hosts are covered: %v", err)
	}
	renewed, err := LoadOrCreateSelfSigned(dir, []string{"localhost", "192.168.1.5", "dcc.example.org"})
	if err != nil || renewed.Fingerprint == first.Fingerprint {
		t.Fatalf("certificate was not renewed for a new host name: %v", err)
	}
	leaf, _ := x509.ParseCertificate(renewed.TLS.Certificate[0])
	if err := leaf.VerifyHostname("dcc.example.org"); err != nil {
		t.Fatalf("new host not in certificate: %v", err)
	}
	if err := leaf.VerifyHostname("192.168.1.5"); err != nil {
		t.Fatalf("IP SAN missing: %v", err)
	}
	if cfg := renewed.ServerConfig(); cfg.MinVersion < tls.VersionTLS12 {
		t.Fatal("TLS minimum version too low")
	}
}
