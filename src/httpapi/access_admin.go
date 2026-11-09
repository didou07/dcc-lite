package httpapi

import (
	"net/http"

	"github.com/didou07/dcc-lite/src/app"
)

func (s *Server) remoteAdminAvailable(w http.ResponseWriter) bool {
	if s.remote != nil || s.app.RemoteAccess == nil {
		writeError(w, http.StatusNotFound, "remote access settings are not available here")
		return false
	}
	return true
}

func (s *Server) getRemoteAccess(w http.ResponseWriter, _ *http.Request) {
	if !s.remoteAdminAvailable(w) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": s.app.RemoteAccess.Status()})
}

func (s *Server) putRemoteAccess(w http.ResponseWriter, r *http.Request) {
	if !s.remoteAdminAvailable(w) {
		return
	}
	var input app.RemoteSettings
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	status, newPassword, err := s.app.RemoteAccess.Apply(input)
	if err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "status": status})
		return
	}
	reply := map[string]any{"status": status}
	if newPassword != "" {
		reply["new_password"] = newPassword
	}
	writeJSON(w, http.StatusOK, reply)
}

type remotePasswordRequest struct {
	Password string `json:"password,omitempty"`
	Generate bool   `json:"generate,omitempty"`
}

func (s *Server) setRemotePassword(w http.ResponseWriter, r *http.Request) {
	if !s.remoteAdminAvailable(w) {
		return
	}
	var input remotePasswordRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	reply := map[string]any{}
	switch {
	case input.Generate == (input.Password != ""):
		writeError(w, http.StatusBadRequest, "send either a password or generate: true")
		return
	case input.Generate:
		password, err := s.app.RemoteAccess.GeneratePassword()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		reply["new_password"] = password
	default:
		if err := s.app.RemoteAccess.SetPassword(input.Password); err != nil {
			writeError(w, http.StatusUnprocessableEntity, err.Error())
			return
		}
	}
	reply["status"] = s.app.RemoteAccess.Status()
	writeJSON(w, http.StatusOK, reply)
}
