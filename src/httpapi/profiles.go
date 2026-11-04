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

func (s *Server) exportProfiles(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Disposition", `attachment; filename="dcc-lite-profiles.json"`)
	writeJSON(w, http.StatusOK, map[string]any{"schema_version": config.SchemaVersion, "profiles": s.app.Profiles.List()})
}

// importedProfile is a profile from an export file. Exports made before SSH was
// removed still carry ssh_port; it is accepted and dropped.
type importedProfile struct {
	config.Profile
	LegacySSHPort int `json:"ssh_port,omitempty"`
}

type profileImportRequest struct {
	SchemaVersion int               `json:"schema_version"`
	Profiles      []importedProfile `json:"profiles"`
}

func (s *Server) importProfiles(w http.ResponseWriter, r *http.Request) {
	var input profileImportRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.SchemaVersion != 1 && input.SchemaVersion != config.SchemaVersion {
		writeError(w, http.StatusUnprocessableEntity, "unsupported profile export version")
		return
	}
	profiles := make([]config.Profile, len(input.Profiles))
	for i, p := range input.Profiles {
		profiles[i] = p.Profile
	}
	imported, err := s.app.Profiles.Import(profiles)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"imported": len(imported), "profiles": imported})
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
	// WebPort and WebHTTPS describe the receiver's web interface (OpenWebif),
	// used by the live screen and "Reload on receiver". 0 means 80 (443 for HTTPS).
	WebPort  int    `json:"web_port"`
	WebHTTPS bool   `json:"web_https"`
	Username string `json:"username"`
	Password string `json:"password"`
	Enabled  *bool  `json:"enabled"`
}

func (s *Server) createProfile(w http.ResponseWriter, r *http.Request) {
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
		// Receivers use self-signed certificates; ticking the box opts in to that.
		p.Scheme, p.InsecureTLS = "https", true
	}
	return p
}
