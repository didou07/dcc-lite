package httpapi

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/logx"
)

var actLog = logx.For("action")

func logAction(w http.ResponseWriter, r *http.Request, what string) {
	args := []any{"req", w.Header().Get("X-Request-ID")}
	if id := r.PathValue("id"); id != "" {
		args = append([]any{"id", id}, args...)
	}
	actLog.Info(what, args...)
}

func (s *Server) registerLogRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/logs", s.getLogs)
	mux.HandleFunc("DELETE /api/v1/logs", s.clearLogs)
	mux.HandleFunc("GET /api/v1/logs/download", s.downloadLogs)
	mux.HandleFunc("GET /api/v1/logs/config", s.getLogConfig)
	mux.HandleFunc("PUT /api/v1/logs/config", s.putLogConfig)
	mux.HandleFunc("POST /api/v1/logs/client", s.clientLog)
	mux.HandleFunc("GET /api/v1/faults", s.getFaults)
	mux.HandleFunc("DELETE /api/v1/faults", s.clearFaults)
	mux.HandleFunc("POST /api/v1/faults/ack-all", s.ackAllFaults)
	mux.HandleFunc("POST /api/v1/faults/{id}/ack", s.ackFault)
}

func queryFromRequest(r *http.Request, defaultLimit int) logx.Query {
	v := r.URL.Query()
	q := logx.Query{Text: strings.TrimSpace(v.Get("q")), Limit: defaultLimit}
	if n, err := strconv.ParseUint(v.Get("since"), 10, 64); err == nil {
		q.Since = n
	}
	if n, err := strconv.Atoi(v.Get("limit")); err == nil && n > 0 && n <= 5000 {
		q.Limit = n
	}
	if lv := v.Get("level"); lv != "" {
		if l, err := logx.ParseLevel(lv); err == nil {
			q.MinLevel = logx.LevelRank(logx.LevelName(l))
		}
	}
	if c := v.Get("component"); c != "" {
		q.Components = map[string]bool{}
		for _, name := range strings.Split(c, ",") {
			if name = strings.TrimSpace(name); name != "" {
				q.Components[name] = true
			}
		}
	}
	return q
}

func (s *Server) getLogs(w http.ResponseWriter, r *http.Request) {
	hub := logx.Default()
	entries, last := hub.Ring().Snapshot(queryFromRequest(r, 500))
	levels, components := hub.Ring().Counts()
	if entries == nil {
		entries = []logx.Entry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries, "last_id": last, "capacity": hub.Ring().Capacity(),
		"counts": map[string]any{"levels": levels, "components": components},
		"faults": hub.Faults().Stats(),
	})
}

func (s *Server) clearLogs(w http.ResponseWriter, _ *http.Request) {
	logx.Default().Ring().Clear()
	logx.For("app").Info("log view cleared")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) logConfig() map[string]any {
	hub := logx.Default()
	return map[string]any{
		"level":      logx.LevelName(hub.ConsoleLevel()),
		"file_level": logx.LevelName(hub.FileLevel()),
		"file":       hub.FilePath(),
		"dir":        hub.Dir(),
		"json":       hub.JSON(),
		"levels":     logx.LevelNames,
	}
}

func (s *Server) getLogConfig(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.logConfig())
}

func (s *Server) putLogConfig(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level     *string `json:"level"`
		FileLevel *string `json:"file_level"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	hub := logx.Default()
	if body.Level != nil {
		l, err := logx.ParseLevel(*body.Level)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		hub.SetConsoleLevel(l)
	}
	if body.FileLevel != nil {
		l, err := logx.ParseLevel(*body.FileLevel)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		hub.SetFileLevel(l)
	}
	logx.For("app").Info("log level changed", "console", logx.LevelName(hub.ConsoleLevel()), "file", logx.LevelName(hub.FileLevel()))
	writeJSON(w, http.StatusOK, s.logConfig())
}

func (s *Server) downloadLogs(w http.ResponseWriter, r *http.Request) {
	report := logx.Default().Report(s.app.SystemInfo(), queryFromRequest(r, 5000))
	name := "dcclite-report-" + time.Now().Format("20060102-150405") + ".txt"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(report))
}

var clientLimiter struct {
	sync.Mutex
	window time.Time
	count  int
}

func allowClientLog() bool {
	l := &clientLimiter
	l.Lock()
	defer l.Unlock()
	now := time.Now()
	if now.Sub(l.window) > time.Minute {
		l.window, l.count = now, 0
	}
	l.count++
	return l.count <= 30
}

func clip(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n] + "..."
	}
	return s
}

func (s *Server) clientLog(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Level   string `json:"level"`
		Message string `json:"message"`
		Source  string `json:"source"`
		Line    int    `json:"line"`
		Column  int    `json:"column"`
		Stack   string `json:"stack"`
		View    string `json:"view"`
	}
	if err := decodeJSON(w, r, &body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !allowClientLog() {
		writeError(w, http.StatusTooManyRequests, "too many client log messages")
		return
	}
	level := logx.LevelError
	if body.Level != "" {
		if l, err := logx.ParseLevel(body.Level); err == nil {
			level = l
		}
	}
	msg := clip(body.Message, 300)
	if msg == "" {
		msg = "unknown browser error"
	}
	args := []any{"view", clip(body.View, 30)}
	if body.Source != "" {
		args = append(args, "source", clip(body.Source, 120), "line", body.Line, "col", body.Column)
	}
	if body.Stack != "" {
		args = append(args, "stack", clip(body.Stack, 3000))
	}
	logx.For("ui").Log(r.Context(), level, msg, args...)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) getFaults(w http.ResponseWriter, _ *http.Request) {
	list, st := logx.Default().Faults().List()
	writeJSON(w, http.StatusOK, map[string]any{"faults": list, "stats": st})
}

func (s *Server) ackFault(w http.ResponseWriter, r *http.Request) {
	if !logx.Default().Faults().Ack(r.PathValue("id")) {
		writeError(w, http.StatusNotFound, "fault not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "stats": logx.Default().Faults().Stats()})
}

func (s *Server) ackAllFaults(w http.ResponseWriter, _ *http.Request) {
	logx.Default().Faults().AckAll()
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "stats": logx.Default().Faults().Stats()})
}

func (s *Server) clearFaults(w http.ResponseWriter, _ *http.Request) {
	logx.Default().Faults().Clear()
	logx.For("app").Info("fault list cleared")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
