package httpapi

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/logx"
)

var httpLog = logx.For("http")

var quietPrefixes = []string{
	"/js/", "/vendor/", "/app.css", "/favicon", "/login", "/api/v1/health",
	"/api/v1/connections", "/api/v1/files/transfers", "/api/v1/logs", "/api/v1/faults",
	"/api/v1/auth/status",
}

func isQuiet(path string) bool {
	if path == "/" || path == "/index.html" {
		return true
	}
	for _, p := range quietPrefixes {
		if strings.HasPrefix(path, p) {
			return true
		}
	}
	return strings.HasSuffix(path, "/screen") || strings.HasSuffix(path, "/signal") || strings.HasSuffix(path, "/picon") || strings.HasSuffix(path, "/boximage")
}

func newRequestID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(b[:])
}

type recorder struct {
	http.ResponseWriter
	status  int
	bytes   int64
	errBody bytes.Buffer
}

func (r *recorder) WriteHeader(code int) {
	if r.status == 0 {
		r.status = code
	}
	r.ResponseWriter.WriteHeader(code)
}

func (r *recorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	if r.status >= 400 && r.errBody.Len() < 1024 {
		r.errBody.Write(p[:min(len(p), 1024-r.errBody.Len())])
	}
	n, err := r.ResponseWriter.Write(p)
	r.bytes += int64(n)
	return n, err
}

func (r *recorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (r *recorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := r.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, errors.New("hijacking is not supported")
	}
	if r.status == 0 {
		r.status = http.StatusSwitchingProtocols
	}
	return h.Hijack()
}

func (r *recorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *recorder) errorText() string {
	var body struct {
		Error string `json:"error"`
	}
	if json.Unmarshal(r.errBody.Bytes(), &body) == nil && body.Error != "" {
		return body.Error
	}
	return ""
}

func observe(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		w.Header().Set("X-Request-ID", id)
		rec := &recorder{ResponseWriter: w}
		start := time.Now()

		defer func() {
			if p := recover(); p != nil {
				if p == http.ErrAbortHandler {
					panic(p)
				}
				logx.Panic(httpLog, p, "request handler", "req", id, "method", r.Method, "route", routeOf(r), "path", r.URL.Path)
				if rec.status == 0 {
					writeError(rec, http.StatusInternalServerError, "internal error (see Tools > Logs, request "+id+")")
				}
				return
			}
			status := rec.status
			if status == 0 {
				status = http.StatusOK
			}
			if status < 400 && r.Method == http.MethodGet && (strings.HasPrefix(r.URL.Path, "/api/v1/logs") || strings.HasPrefix(r.URL.Path, "/api/v1/faults")) {
				return
			}
			level := levelForStatus(status, isQuiet(r.URL.Path))
			if !httpLog.Enabled(r.Context(), level) {
				return
			}
			args := []any{"status", status, "ms", time.Since(start).Milliseconds(), "req", id}
			if rt := routeOf(r); rt != "" && status >= 400 {
				args = append(args, "route", rt)
			}
			if rec.bytes > 0 && level <= logx.LevelDebug {
				args = append(args, "bytes", rec.bytes)
			}
			if status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests {
				args = append(args, "remote", remoteHost(r))
			}
			if msg := rec.errorText(); msg != "" {
				args = append(args, "error", msg)
			}
			httpLog.Log(r.Context(), level, r.Method+" "+r.URL.Path, args...)
		}()
		next.ServeHTTP(rec, r)
	})
}

func routeOf(r *http.Request) string {
	if r.Pattern == "" {
		return ""
	}
	return r.Pattern
}

func levelForStatus(status int, quiet bool) slog.Level {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden || status == http.StatusTooManyRequests:
		return logx.LevelWarn
	case status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout:
		return logx.LevelWarn
	case status >= 500:
		return logx.LevelError
	case status >= 400:
		return logx.LevelInfo
	case quiet:
		return logx.LevelTrace
	}
	return logx.LevelDebug
}

func remoteHost(r *http.Request) string {
	if host, _, err := net.SplitHostPort(r.RemoteAddr); err == nil {
		return host
	}
	return r.RemoteAddr
}

func rejected(r *http.Request, reason string) {
	logx.For("auth").Warn("request rejected", "reason", reason, "method", r.Method, "path", r.URL.Path, "remote", remoteHost(r))
}
