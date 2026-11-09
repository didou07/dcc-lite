package httpapi

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/remoteaccess"
	"github.com/didou07/dcc-lite/src/terminal"
)

type terminalOpenRequest struct {
	ProfileID        string `json:"profile_id"`
	Protocol         string `json:"protocol"`
	Port             int    `json:"port"`
	Columns          int    `json:"columns"`
	Rows             int    `json:"rows"`
	InitialDirectory string `json:"initial_directory,omitempty"`
	InitialCommand   string `json:"initial_command,omitempty"`
}

func (s *Server) openTerminal(w http.ResponseWriter, r *http.Request) {
	var input terminalOpenRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	profile, ok := s.app.Profiles.Get(input.ProfileID)
	if !ok {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	if strings.TrimSpace(profile.Username) == "" {
		profile.Username = "root"
	}
	if input.Port == 0 {
		input.Port = 23
	}
	if input.Columns == 0 {
		input.Columns = 100
	}
	if input.Rows == 0 {
		input.Rows = 30
	}
	if len(input.InitialDirectory) > 4096 || strings.ContainsAny(input.InitialDirectory, "\r\n\x00") {
		writeError(w, http.StatusUnprocessableEntity, "remote directory contains unsupported characters or exceeds 4096 bytes")
		return
	}
	if len(input.InitialCommand) > 8192 || strings.ContainsAny(input.InitialCommand, "\r\n\x00") {
		writeError(w, http.StatusUnprocessableEntity, "initial command must be a single line and no longer than 8192 bytes")
		return
	}
	info, err := s.app.Terminals.Open(r.Context(), profile, input.Protocol, input.Port, input.Columns, input.Rows)
	if err != nil {
		switch {
		case errors.Is(err, terminal.ErrSessionLimit):
			writeError(w, http.StatusTooManyRequests, err.Error())
		default:
			writeError(w, http.StatusBadGateway, err.Error())
		}
		return
	}
	if input.InitialDirectory != "" || strings.TrimSpace(input.InitialCommand) != "" {
		if err := s.app.Terminals.SetInitialDirectoryAndCommand(info.ID, input.InitialDirectory, input.InitialCommand); err != nil {
			_ = s.app.Terminals.Close(info.ID)
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": info, "websocket": "/api/v1/terminal/sessions/" + info.ID + "/ws"})
}

func (s *Server) closeTerminal(w http.ResponseWriter, r *http.Request) {
	if err := s.app.Terminals.Close(r.PathValue("id")); err != nil {
		if errors.Is(err, terminal.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, "terminal session not found")
			return
		}
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) terminalStatus(w http.ResponseWriter, r *http.Request) {
	info, ok := s.app.Terminals.Info(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "terminal session not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"session": info})
}

func (s *Server) terminalWebSocket(w http.ResponseWriter, r *http.Request) {
	originOK := validWebSocketOrigin(r)
	if s.remote != nil {
		originOK = remoteaccess.SameOriginFor(r, !s.remote.auth.Plain())
	}
	if !originOK {
		http.Error(w, "a matching Origin header is required for terminal WebSocket connections", http.StatusForbidden)
		return
	}
	id := r.PathValue("id")

	var offset int64
	if raw := r.URL.Query().Get("offset"); raw != "" {
		value, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil || value < 0 {
			writeError(w, http.StatusBadRequest, "offset must be a non-negative number")
			return
		}
		offset = value
	}
	backend, _, err := s.app.Terminals.AttachAt(id, offset)
	if err != nil {
		if errors.Is(err, terminal.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, "terminal session not found")
		} else if errors.Is(err, terminal.ErrSessionAttached) {
			writeError(w, http.StatusConflict, err.Error())
		} else {
			writeError(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	ws, err := upgradeWebSocket(w, r)
	if err != nil {
		_ = backend.Close()
		return
	}

	endSession := false
	defer func() {
		_ = ws.Close()
		_ = backend.Close()
		if endSession {
			_ = s.app.Terminals.Close(id)
		}
	}()
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	go func() {
		ticker := time.NewTicker(20 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := ws.writePing(); err != nil {
					cancel()
					return
				}

				if s.remote != nil {
					s.remote.auth.Authenticated(r)
				}
			}
		}
	}()
	if lost := backend.Lost(); lost > 0 {
		_ = ws.WriteJSON(map[string]any{"type": "notice", "message": "Some terminal output was missed while the connection was down."})
	}
	type result struct {
		err   error
		final bool
	}
	done := make(chan result, 2)
	go func() {
		for {
			var message struct {
				Type string `json:"type"`
				Data string `json:"data"`
				Cols int    `json:"cols"`
				Rows int    `json:"rows"`
			}
			if err := ws.ReadJSON(&message); err != nil {
				done <- result{err: err}
				return
			}
			switch message.Type {
			case "input":
				if len(message.Data) > 8192 {
					done <- result{err: errors.New("terminal input exceeds size limit")}
					return
				}
				if _, err := backend.Write([]byte(message.Data)); err != nil {
					done <- result{err: err, final: true}
					return
				}
			case "resize":
				if terminal.ValidateDimensions(message.Cols, message.Rows) != nil {
					continue
				}
				if err := backend.Resize(message.Cols, message.Rows); err != nil {
					_ = ws.WriteJSON(map[string]any{"type": "notice", "message": err.Error()})
				}
			case "ping":

				_ = ws.WriteJSON(map[string]any{"type": "pong"})
			case "close":
				done <- result{final: true}
				return
			default:
				done <- result{err: errors.New("unsupported terminal message type")}
				return
			}
		}
	}()
	go func() {
		buffer := make([]byte, 4096)
		for {
			n, readErr := backend.Read(buffer)
			if n > 0 {
				if err := ws.WriteJSON(map[string]any{"type": "output", "data": base64.StdEncoding.EncodeToString(buffer[:n]), "encoding": "base64", "end": backend.Position()}); err != nil {
					done <- result{err: err}
					return
				}
			}
			if readErr != nil {
				if errors.Is(readErr, terminal.ErrDetached) {
					done <- result{err: readErr}
					return
				}

				_ = ws.WriteJSON(map[string]any{"type": "closed"})
				done <- result{err: readErr, final: true}
				return
			}
		}
	}()
	select {
	case <-ctx.Done():
	case res := <-done:
		endSession = res.final
		cancel()
	}
}
