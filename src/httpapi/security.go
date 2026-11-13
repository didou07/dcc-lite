package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

func validWebSocketOrigin(r *http.Request) bool {
	origin := strings.TrimSpace(r.Header.Get("Origin"))
	if origin == "" || r.Host == "" {
		return false
	}
	u, err := url.Parse(origin)
	if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	return isLoopbackHost(u.Host) && strings.EqualFold(u.Host, r.Host)
}

func localOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if !isLoopbackHost(host) {
			rejected(r, "non-local Host header")
			http.Error(w, "local connections only", http.StatusForbidden)
			return
		}
		if origin := r.Header.Get("Origin"); origin != "" {
			u, err := url.Parse(strings.TrimSpace(origin))
			if err != nil || u.Scheme != "http" || u.Host == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || !isLoopbackHost(u.Host) || !strings.EqualFold(u.Host, host) {
				rejected(r, "cross-origin request")
				http.Error(w, "cross-origin request rejected", http.StatusForbidden)
				return
			}
		}
		if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") {
			rejected(r, "cross-site request")
			http.Error(w, "cross-site request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isLoopbackHost(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = strings.Trim(hostport, "[]")
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

const baseCSP = "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self'; img-src 'self' data: blob:; connect-src %s; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'"

func localCSP(*http.Request) string {
	return strings.Replace(baseCSP, "%s", "'self' ws://127.0.0.1:* ws://localhost:* wss://127.0.0.1:* wss://localhost:*", 1)
}

func secureHeaders(next http.Handler) http.Handler { return securityHeaders(localCSP, next) }

func securityHeaders(csp func(*http.Request) string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cross-Origin-Resource-Policy", "same-origin")
		w.Header().Set("Cross-Origin-Opener-Policy", "same-origin")
		w.Header().Set("X-Permitted-Cross-Domain-Policies", "none")
		w.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
		w.Header().Set("Content-Security-Policy", csp(r))
		w.Header().Set("X-DCC-Server-Time", time.Now().UTC().Format(time.RFC3339))
		next.ServeHTTP(w, r)
	})
}
