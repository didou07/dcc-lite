package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestIsLoopbackHost(t *testing.T) {
	good := []string{"localhost", "LOCALHOST:8080", "127.0.0.1", "127.0.0.1:9000", "127.5.5.5", "[::1]", "[::1]:8080", "::1"}
	bad := []string{"", "example.com", "192.168.1.5:8080", "0.0.0.0", "localhost.evil.com", "evil.com:80", "127.0.0.1.evil.com", "10.0.0.1"}
	for _, h := range good {
		if !isLoopbackHost(h) {
			t.Errorf("%q should be loopback", h)
		}
	}
	for _, h := range bad {
		if isLoopbackHost(h) {
			t.Errorf("%q must not be loopback", h)
		}
	}
}

func serve(h http.Handler, host string, headers map[string]string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "http://"+host+"/api/x", nil)
	r.Host = host
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLocalOnlyBlocksRebindingAndCrossSite(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	h := localOnly(ok)

	allowed := []struct {
		host    string
		headers map[string]string
	}{
		{"127.0.0.1:8080", nil},
		{"localhost:8080", map[string]string{"Origin": "http://localhost:8080"}},
		{"127.0.0.1:8080", map[string]string{"Origin": "http://127.0.0.1:8080", "Sec-Fetch-Site": "same-origin"}},
	}
	for _, c := range allowed {
		if w := serve(h, c.host, c.headers); w.Code != 204 {
			t.Errorf("%v: status %d, want 204", c, w.Code)
		}
	}

	blocked := []struct {
		name    string
		host    string
		headers map[string]string
	}{
		{"DNS rebinding Host", "attacker.example:8080", nil},
		{"LAN Host", "192.168.1.5:8080", nil},
		{"cross-origin page", "127.0.0.1:8080", map[string]string{"Origin": "https://evil.example"}},
		{"other local port as origin", "127.0.0.1:8080", map[string]string{"Origin": "http://127.0.0.1:9999"}},
		{"https origin", "127.0.0.1:8080", map[string]string{"Origin": "https://127.0.0.1:8080"}},
		{"origin with path", "127.0.0.1:8080", map[string]string{"Origin": "http://127.0.0.1:8080/x"}},
		{"origin with userinfo", "127.0.0.1:8080", map[string]string{"Origin": "http://a@127.0.0.1:8080"}},
		{"cross-site fetch", "127.0.0.1:8080", map[string]string{"Sec-Fetch-Site": "cross-site"}},
	}
	for _, c := range blocked {
		if w := serve(h, c.host, c.headers); w.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", c.name, w.Code)
		}
	}
}

func TestValidWebSocketOrigin(t *testing.T) {
	mk := func(host, origin string) *http.Request {
		r := httptest.NewRequest("GET", "http://"+host+"/ws", nil)
		r.Host = host
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		return r
	}
	if !validWebSocketOrigin(mk("127.0.0.1:8080", "http://127.0.0.1:8080")) {
		t.Error("same-origin loopback rejected")
	}
	for _, c := range [][2]string{
		{"127.0.0.1:8080", ""},
		{"127.0.0.1:8080", "http://evil.example"},
		{"127.0.0.1:8080", "http://127.0.0.1:1"},
		{"evil.example", "http://evil.example"}, // same origin but not loopback
		{"127.0.0.1:8080", "null"},
	} {
		if validWebSocketOrigin(mk(c[0], c[1])) {
			t.Errorf("%v accepted", c)
		}
	}
}

func TestSecurityHeadersAreAlwaysSet(t *testing.T) {
	h := secureHeaders(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	w := serve(h, "127.0.0.1:8080", nil)
	for _, name := range []string{"X-Content-Type-Options", "X-Frame-Options", "Referrer-Policy", "Content-Security-Policy",
		"Cross-Origin-Resource-Policy", "Permissions-Policy", "Cross-Origin-Opener-Policy"} {
		if w.Header().Get(name) == "" {
			t.Errorf("%s missing", name)
		}
	}
	csp := w.Header().Get("Content-Security-Policy")
	for _, want := range []string{"default-src 'self'", "object-src 'none'", "frame-ancestors 'none'", "base-uri 'none'"} {
		if !contains(csp, want) {
			t.Errorf("CSP lacks %q: %s", want, csp)
		}
	}
	if contains(csp, "script-src 'self' 'unsafe") || contains(csp, "'unsafe-eval'") {
		t.Errorf("CSP allows unsafe scripts: %s", csp)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
