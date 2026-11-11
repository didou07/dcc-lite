package httpapi

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/logx"
	"github.com/didou07/dcc-lite/src/remoteaccess"
	webui "github.com/didou07/dcc-lite/src/web"
)

type remoteMode struct {
	auth *remoteaccess.Auth
}

type RemoteOptions struct {
	Auth *remoteaccess.Auth
}

func NewRemote(application *app.App, options RemoteOptions) http.Handler {
	s := &Server{app: application, remote: &remoteMode{auth: options.Auth}}
	protected := s.requireSignIn(s.routes())

	root := http.NewServeMux()
	static := s.staticFiles()
	root.HandleFunc("GET /login", s.loginPage)
	root.Handle("GET /login.js", static)
	root.Handle("GET /app.css", static)
	root.HandleFunc("POST /api/v1/auth/login", s.login)
	root.HandleFunc("POST /api/v1/auth/logout", s.logout)
	root.HandleFunc("GET /api/v1/auth/status", s.authStatus)
	root.Handle("/", protected)
	return securityHeaders(s.remoteCSP, s.remoteGuard(observe(root)))
}

var safeHostPattern = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]{1,255}$`)

func (s *Server) remoteCSP(r *http.Request) string {
	connect := "'self'"
	if safeHostPattern.MatchString(r.Host) {
		scheme := "wss://"
		if s.remote.auth.Plain() {
			scheme = "ws://"
		}
		connect += " " + scheme + r.Host
	}
	return strings.Replace(baseCSP, "%s", connect, 1)
}

func (s *Server) remoteGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == "" {
			rejected(r, "missing Host header")
			http.Error(w, "missing Host header", http.StatusBadRequest)
			return
		}
		if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") && r.Header.Get("Sec-Fetch-Mode") != "navigate" {
			rejected(r, "cross-site request")
			http.Error(w, "cross-site request rejected", http.StatusForbidden)
			return
		}
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !remoteaccess.SameOriginFor(r, !s.remote.auth.Plain()) {
				rejected(r, "cross-origin request")
				http.Error(w, "cross-origin request rejected", http.StatusForbidden)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireSignIn(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.remote.auth.Authenticated(r) {
			w.Header().Set("Cache-Control", "no-store")
			next.ServeHTTP(w, r)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "sign in required", "code": "auth_required"})
			return
		}
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		http.Error(w, "sign in required", http.StatusUnauthorized)
	})
}

func (s *Server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.remote.auth.Authenticated(r) {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	page, err := webui.Page("login.html")
	if err != nil {
		http.Error(w, "login page unavailable", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(page)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid sign-in request")
		return
	}
	err := s.remote.auth.Login(w, r, input.Password)
	var throttled remoteaccess.ErrThrottled
	switch {
	case err == nil:
		logx.For("auth").Info("remote sign-in", "remote", remoteHost(r))
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	case errors.Is(err, remoteaccess.ErrBadPassword):
		writeError(w, http.StatusUnauthorized, "Incorrect password.")
	case errors.As(err, &throttled):
		w.Header().Set("Retry-After", strconv.Itoa(int(throttled.RetryAfter.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "Too many failed attempts. Wait a few minutes and try again.")
	default:
		writeError(w, http.StatusInternalServerError, "sign-in is unavailable")
	}
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	s.remote.auth.Logout(w, r)
	logx.For("auth").Info("remote sign-out", "remote", remoteHost(r))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) authStatus(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": s.remote.auth.Authenticated(r)})
}
