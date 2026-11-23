package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/didou07/dcc-lite/src/updater"
)

func (s *Server) registerUpdateRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/update", s.getUpdate)
	mux.HandleFunc("POST /api/v1/update/check", s.checkUpdate)
	mux.HandleFunc("POST /api/v1/update/install", s.installUpdate)
	mux.HandleFunc("PUT /api/v1/update/settings", s.putUpdateSettings)
}

func (s *Server) updateWritable(w http.ResponseWriter) bool {
	if s.app.Updates == nil {
		writeError(w, http.StatusNotFound, "updates are not available")
		return false
	}
	if s.remote != nil {
		writeError(w, http.StatusForbidden, "updates can only be managed from the computer running DCC Lite")
		return false
	}
	return true
}

func (s *Server) getUpdate(w http.ResponseWriter, _ *http.Request) {
	if s.app.Updates == nil {
		writeError(w, http.StatusNotFound, "updates are not available")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"update": s.app.Updates.Status(), "can_manage": s.remote == nil})
}

func (s *Server) checkUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.updateWritable(w) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	status, err := s.app.Updates.Check(ctx)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"update": status, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"update": status})
}

func (s *Server) installUpdate(w http.ResponseWriter, r *http.Request) {
	if !s.updateWritable(w) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	longTransfer(w)
	if err := s.app.Updates.Install(ctx); err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		} else if errors.Is(err, updater.ErrActive) {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"update": s.app.Updates.Status(), "restarting": true})
}

func (s *Server) putUpdateSettings(w http.ResponseWriter, r *http.Request) {
	if !s.updateWritable(w) {
		return
	}
	var input struct {
		Notify bool `json:"notify"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.app.Updates.SetNotify(input.Notify); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"update": s.app.Updates.Status()})
}
