package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/remoteaccess"
)

const (
	remoteHost     = "box.local:8788"
	remotePassword = "correct horse battery"
)

func remoteTestServer(t *testing.T) http.Handler {
	t.Helper()
	handler, _ := remoteTestServerWithApp(t)
	return handler
}

func remoteTestServerWithApp(t *testing.T) (http.Handler, *app.App) {
	t.Helper()
	store, err := config.NewProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	application := app.New("test", store, t.TempDir())
	t.Cleanup(application.Terminals.CloseAll)
	t.Cleanup(application.Files.CloseAll)
	creds, err := remoteaccess.OpenCredentials(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := creds.Set(remotePassword); err != nil {
		t.Fatal(err)
	}
	return NewRemote(application, RemoteOptions{Auth: remoteaccess.NewAuth(creds)}), application
}

func remoteDo(handler http.Handler, method, target string, body []byte, mutate func(*http.Request)) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "https://"+remoteHost+target, bytes.NewReader(body))
	req.RemoteAddr = "192.168.1.50:40000"
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if mutate != nil {
		mutate(req)
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func sameOrigin(req *http.Request) { req.Header.Set("Origin", "https://"+remoteHost) }

func signIn(t *testing.T, handler http.Handler) *http.Cookie {
	t.Helper()
	body, _ := json.Marshal(map[string]string{"password": remotePassword})
	rec := remoteDo(handler, http.MethodPost, "/api/v1/auth/login", body, sameOrigin)
	if rec.Code != http.StatusOK {
		t.Fatalf("sign-in status %d: %s", rec.Code, rec.Body)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("expected one session cookie, got %v", cookies)
	}
	return cookies[0]
}

func TestRemoteRequiresSignInForEverythingExceptLogin(t *testing.T) {
	h := remoteTestServer(t)
	rec := remoteDo(h, http.MethodGet, "/", nil, nil)
	if rec.Code != http.StatusFound || rec.Header().Get("Location") != "/login" {
		t.Fatalf("GET / = %d %q, want redirect to /login", rec.Code, rec.Header().Get("Location"))
	}
	for _, target := range []string{"/api/v1/profiles", "/api/v1/system", "/api/v1/files/local/list", "/api/v1/profiles/export", "/api/v1/health"} {
		rec := remoteDo(h, http.MethodGet, target, nil, nil)
		if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "auth_required") {
			t.Errorf("%s = %d %s, want 401 auth_required", target, rec.Code, rec.Body)
		}
	}
	for _, target := range []string{"/js/main.js", "/index.html", "/vendor/xterm.js"} {
		if rec := remoteDo(h, http.MethodGet, target, nil, nil); rec.Code != http.StatusFound {
			t.Errorf("%s served without sign-in: %d", target, rec.Code)
		}
	}
	for _, target := range []string{"/login", "/login.js", "/app.css"} {
		if rec := remoteDo(h, http.MethodGet, target, nil, nil); rec.Code != http.StatusOK {
			t.Errorf("%s = %d, want 200 before sign-in", target, rec.Code)
		}
	}
	if rec := remoteDo(h, http.MethodPost, "/api/v1/profiles", []byte(`{}`), sameOrigin); rec.Code != http.StatusUnauthorized {
		t.Errorf("unauthenticated write = %d, want 401", rec.Code)
	}
}

func TestRemoteLoginRejectsBadPasswordAndForeignOrigins(t *testing.T) {
	h := remoteTestServer(t)
	body, _ := json.Marshal(map[string]string{"password": "not the password"})
	if rec := remoteDo(h, http.MethodPost, "/api/v1/auth/login", body, sameOrigin); rec.Code != http.StatusUnauthorized || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("bad password = %d cookies=%v", rec.Code, rec.Result().Cookies())
	}
	good, _ := json.Marshal(map[string]string{"password": remotePassword})
	if rec := remoteDo(h, http.MethodPost, "/api/v1/auth/login", good, nil); rec.Code != http.StatusForbidden {
		t.Fatalf("login without Origin = %d, want 403", rec.Code)
	}
	evil := func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }
	if rec := remoteDo(h, http.MethodPost, "/api/v1/auth/login", good, evil); rec.Code != http.StatusForbidden {
		t.Fatalf("login from foreign origin = %d, want 403", rec.Code)
	}
}

func TestRemoteSessionGrantsAccessAndLogoutRevokesIt(t *testing.T) {
	h := remoteTestServer(t)
	cookie := signIn(t, h)
	withCookie := func(r *http.Request) { r.AddCookie(cookie); sameOrigin(r) }

	rec := remoteDo(h, http.MethodGet, "/api/v1/system", nil, withCookie)
	if rec.Code != http.StatusOK {
		t.Fatalf("system = %d: %s", rec.Code, rec.Body)
	}
	var info map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &info)
	if info["remote"] != "true" {
		t.Fatalf("system info must flag remote mode: %v", info)
	}
	if rec := remoteDo(h, http.MethodGet, "/", nil, withCookie); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "DCC Lite") {
		t.Fatalf("UI not served after sign-in: %d", rec.Code)
	}
	if rec := remoteDo(h, http.MethodGet, "/login", nil, withCookie); rec.Code != http.StatusFound {
		t.Fatalf("signed-in /login = %d, want redirect", rec.Code)
	}

	// A signed-in session still may not write from a foreign origin or without Origin.
	create := []byte(`{"name":"Box","host":"192.168.1.10"}`)
	evil := func(r *http.Request) { r.AddCookie(cookie); r.Header.Set("Origin", "https://evil.example") }
	if rec := remoteDo(h, http.MethodPost, "/api/v1/profiles", create, evil); rec.Code != http.StatusForbidden {
		t.Fatalf("cross-origin write with valid session = %d, want 403", rec.Code)
	}
	if rec := remoteDo(h, http.MethodPost, "/api/v1/profiles", create, func(r *http.Request) { r.AddCookie(cookie) }); rec.Code != http.StatusForbidden {
		t.Fatalf("write without Origin = %d, want 403", rec.Code)
	}
	if rec := remoteDo(h, http.MethodPost, "/api/v1/profiles", create, withCookie); rec.Code != http.StatusCreated {
		t.Fatalf("same-origin write = %d: %s", rec.Code, rec.Body)
	}

	if rec := remoteDo(h, http.MethodPost, "/api/v1/auth/logout", []byte(`{}`), withCookie); rec.Code != http.StatusNoContent {
		t.Fatalf("logout = %d", rec.Code)
	}
	if rec := remoteDo(h, http.MethodGet, "/api/v1/profiles", nil, withCookie); rec.Code != http.StatusUnauthorized {
		t.Fatalf("session still valid after logout: %d", rec.Code)
	}
}

func TestRemotePinsSecurityHeaders(t *testing.T) {
	h := remoteTestServer(t)
	cookie := signIn(t, h)
	withCookie := func(r *http.Request) { r.AddCookie(cookie); sameOrigin(r) }

	rec := remoteDo(h, http.MethodGet, "/api/v1/system", nil, withCookie)
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "wss://"+remoteHost) || strings.Contains(csp, "127.0.0.1") || !strings.Contains(csp, "script-src 'self'") {
		t.Fatalf("remote CSP = %q", csp)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("authenticated responses must not be cached: %q", rec.Header().Get("Cache-Control"))
	}
}

func TestRemoteTerminalWebSocketNeedsSameOrigin(t *testing.T) {
	h := remoteTestServer(t)
	cookie := signIn(t, h)
	target := "/api/v1/terminal/sessions/unknown/ws"
	foreign := func(r *http.Request) { r.AddCookie(cookie); r.Header.Set("Origin", "https://evil.example") }
	if rec := remoteDo(h, http.MethodGet, target, nil, foreign); rec.Code != http.StatusForbidden {
		t.Fatalf("foreign-origin WebSocket = %d, want 403", rec.Code)
	}
	if rec := remoteDo(h, http.MethodGet, target, nil, func(r *http.Request) { r.AddCookie(cookie) }); rec.Code != http.StatusForbidden {
		t.Fatalf("WebSocket without Origin = %d, want 403", rec.Code)
	}
	same := func(r *http.Request) { r.AddCookie(cookie); sameOrigin(r) }
	if rec := remoteDo(h, http.MethodGet, target, nil, same); rec.Code != http.StatusNotFound {
		t.Fatalf("same-origin WebSocket for unknown session = %d, want 404", rec.Code)
	}
	if rec := remoteDo(h, http.MethodGet, target, nil, nil); rec.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated WebSocket = %d, want 401", rec.Code)
	}
}

func TestLoopbackListenerStillRejectsRemoteHostsAndHasNoAuthRoutes(t *testing.T) {
	h := testServer(t)
	if res := request(t, h, http.MethodGet, "/api/v1/auth/status", nil); res.Code == http.StatusOK && strings.Contains(res.Body.String(), "authenticated") {
		t.Fatal("loopback listener must not expose the auth API")
	}
	req := httptest.NewRequest(http.MethodGet, "http://"+remoteHost+"/api/v1/health", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("non-loopback Host on the loopback listener = %d, want 403", rec.Code)
	}
}
