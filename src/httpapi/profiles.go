package httpapi

import (
	"net/http"
	"strings"

	"github.com/didou07/dcc-lite/src/config"
)

type profileView struct {
	config.Profile
	CredentialConfigured bool `json:"credential_configured"`
}

func (s *Server) listProfiles(w http.ResponseWriter, _ *http.Request) {
	profiles := s.app.Profiles.List()
	views := make([]profileView, 0, len(profiles))
	for _, profile := range profiles {
		views = append(views, profileView{Profile: profile, CredentialConfigured: profile.Password != ""})
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": views})
}

func (s *Server) revealCredential(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.app.Profiles.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"password": profile.Password})
}

func (s *Server) setCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	profile, ok := s.app.Profiles.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	var input struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	profile.Password = input.Password
	saved, err := s.app.Profiles.Save(profile)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.app.Connections.Forget(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "credential_configured": saved.Password != ""})
}

func (s *Server) deleteCredential(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	profile, ok := s.app.Profiles.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	profile.Password = ""
	if _, err := s.app.Profiles.Save(profile); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.app.Connections.Forget(id)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listConnections(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"connections": s.app.Connections.List()})
}

func (s *Server) testConnection(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	profile, ok := s.app.Profiles.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	status := s.app.Connections.Test(r.Context(), profile)
	writeJSON(w, http.StatusOK, map[string]any{"connection": status})
}

type profileRequest struct {
	Name       string `json:"name"`
	Host       string `json:"host"`
	FTPPort    int    `json:"ftp_port"`
	TelnetPort int    `json:"telnet_port"`

	WebPort  int    `json:"web_port"`
	WebHTTPS bool   `json:"web_https"`
	Username string `json:"username"`
	Password string `json:"password"`
	Enabled  *bool  `json:"enabled"`
}

func (s *Server) createProfile(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "device profile create requested")
	var input profileRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p := profileFromRequest(input)
	if p.Password == "" {
		p.Password = config.DefaultPassword
	}
	saved, err := s.app.Profiles.Save(p)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"profile": saved, "credential_configured": saved.Password != ""})
}

func (s *Server) updateProfile(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "device profile update requested")
	id := r.PathValue("id")
	existing, ok := s.app.Profiles.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	var input profileRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	p := profileFromRequest(input)
	p.ID = existing.ID
	if p.Password == "" {
		p.Password = existing.Password
	}
	saved, err := s.app.Profiles.Save(p)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	s.app.Connections.Forget(id)
	s.app.Terminals.CloseProfile(id)
	s.app.Files.CloseProfile(id)
	writeJSON(w, http.StatusOK, map[string]any{"profile": saved, "credential_configured": saved.Password != ""})
}

func (s *Server) deleteProfile(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "device profile delete requested")
	id := r.PathValue("id")
	if err := s.app.Profiles.Delete(id); err != nil {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	s.app.Connections.Forget(id)
	s.app.Terminals.CloseProfile(id)
	s.app.Files.CloseProfile(id)
	w.WriteHeader(http.StatusNoContent)
}

func profileFromRequest(input profileRequest) config.Profile {
	enabled := true
	if input.Enabled != nil {
		enabled = *input.Enabled
	}
	username := strings.TrimSpace(input.Username)
	if username == "" {
		username = config.DefaultUsername
	}
	p := config.Profile{
		Name: input.Name, Host: input.Host,
		FTPPort: input.FTPPort, TelnetPort: input.TelnetPort,
		Port:     input.WebPort,
		Username: username, Password: input.Password, Enabled: enabled,
	}
	if input.WebHTTPS {

		p.Scheme, p.InsecureTLS = "https", true
	}
	return p
}
