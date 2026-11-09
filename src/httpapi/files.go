package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/filetransfer"
)

type fileSessionRequest struct {
	ProfileID string `json:"profile_id"`
	Protocol  string `json:"protocol"`
	Port      int    `json:"port"`
}

func (s *Server) denyHostFiles(w http.ResponseWriter) bool {
	if s.remote == nil {
		return false
	}
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error": "The files of the computer that runs DCC Lite are not available in a remote session. Use \"Upload files\" and \"Download to this device\" to move files from and to your own device.",
		"code":  "host_files_unavailable",
	})
	return true
}

func (s *Server) listLocalFiles(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	if s.app.LocalFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "local file browser is unavailable")
		return
	}
	entries, current, err := s.app.LocalFiles.List(r.URL.Query().Get("path"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	parent := "."
	if current != "." {
		parent = filepath.ToSlash(filepath.Dir(filepath.FromSlash(current)))
	}
	writeJSON(w, http.StatusOK, map[string]any{"root": s.app.LocalFiles.RootLabel(), "path": current, "parent": parent, "entries": entries})
}

func (s *Server) makeLocalDirectory(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.app.LocalFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "local file browser is unavailable")
		return
	}
	if err := s.app.LocalFiles.Mkdir(input.Path); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) renameLocalFile(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	var input struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.app.LocalFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "local file browser is unavailable")
		return
	}
	if err := s.app.LocalFiles.Rename(input.OldPath, input.NewPath); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeLocalFile(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.app.LocalFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "local file browser is unavailable")
		return
	}
	if err := s.app.LocalFiles.Remove(input.Path); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) chmodLocalFile(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	var input struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode, err := strconv.ParseUint(strings.TrimPrefix(input.Mode, "0"), 8, 12)
	if err != nil {
		writeError(w, http.StatusBadRequest, "mode must be octal, for example 755")
		return
	}
	if s.app.LocalFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "local file browser is unavailable")
		return
	}
	if err := s.app.LocalFiles.Chmod(input.Path, uint32(mode)); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listFileSessions(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"sessions": s.app.Files.Sessions()})
}

func (s *Server) openFileSession(w http.ResponseWriter, r *http.Request) {
	var input fileSessionRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	profile, ok := s.app.Profiles.Get(input.ProfileID)
	if !ok {
		writeError(w, http.StatusNotFound, "device profile not found")
		return
	}
	protocol := strings.ToLower(strings.TrimSpace(input.Protocol))
	if strings.TrimSpace(profile.Username) == "" {
		profile.Username = "root"
	}
	if input.Port == 0 {
		input.Port = 21
	}
	password := profile.Password
	info, err := s.app.Files.Open(r.Context(), profile, protocol, input.Port, password)
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, filetransfer.ErrSessionLimit) {
			status = http.StatusTooManyRequests
		}
		writeError(w, status, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"session": info})
}

func (s *Server) closeFileSession(w http.ResponseWriter, r *http.Request) {
	if err := s.app.Files.Close(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listRemoteFiles(w http.ResponseWriter, r *http.Request) {
	remotePath := r.URL.Query().Get("path")
	if remotePath == "" {
		remotePath = "."
	}
	entries, err := s.app.Files.List(r.Context(), r.PathValue("id"), remotePath)
	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	parent := path.Dir(remotePath)
	if remotePath == "/" || remotePath == "." {
		parent = "/"
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": remotePath, "parent": parent, "entries": entries})
}

func (s *Server) makeRemoteDirectory(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.app.Files.Mkdir(r.Context(), r.PathValue("id"), input.Path); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) renameRemoteFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OldPath string `json:"old_path"`
		NewPath string `json:"new_path"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.app.Files.Rename(r.Context(), r.PathValue("id"), input.OldPath, input.NewPath); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeRemoteFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path      string `json:"path"`
		Directory bool   `json:"directory"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.app.Files.Remove(r.Context(), r.PathValue("id"), input.Path, input.Directory); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) chmodRemoteFile(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path string `json:"path"`
		Mode string `json:"mode"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	mode, err := strconv.ParseUint(strings.TrimPrefix(input.Mode, "0"), 8, 12)
	if err != nil {
		writeError(w, http.StatusBadRequest, "mode must be octal, for example 755")
		return
	}
	if err := s.app.Files.Chmod(r.Context(), r.PathValue("id"), input.Path, uint32(mode)); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) readRemoteText(w http.ResponseWriter, r *http.Request) {
	remotePath := r.URL.Query().Get("path")
	if remotePath == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	file, err := s.app.Files.ReadText(r.Context(), r.PathValue("id"), remotePath)
	if err != nil {
		writeEditError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": file, "max_bytes": filetransfer.MaxEditBytes})
}

func (s *Server) writeRemoteText(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Path               string    `json:"path"`
		Content            string    `json:"content"`
		LineEnding         string    `json:"line_ending"`
		ExpectedSize       int64     `json:"expected_size"`
		ExpectedModifiedAt time.Time `json:"expected_modified_at"`
		ExpectedHash       string    `json:"expected_hash"`
		Force              bool      `json:"force"`
		Create             bool      `json:"create"`
	}

	r.Body = http.MaxBytesReader(w, r.Body, 2*filetransfer.MaxEditBytes+4096)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	file, err := s.app.Files.WriteText(r.Context(), r.PathValue("id"), input.Path, filetransfer.TextWrite{
		Content: input.Content, LineEnding: input.LineEnding,
		ExpectedSize: input.ExpectedSize, ExpectedModifiedAt: input.ExpectedModifiedAt, ExpectedHash: input.ExpectedHash,
		Force: input.Force, Create: input.Create,
	})
	if err != nil {
		writeEditError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": file})
}

func writeEditError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, filetransfer.ErrSessionNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error(), "code": "session_not_found"})
	case errors.Is(err, filetransfer.ErrEditNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error(), "code": "file_not_found"})
	case errors.Is(err, filetransfer.ErrEditConflict):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "conflict"})
	case errors.Is(err, filetransfer.ErrEditExists):
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "exists"})
	case errors.Is(err, filetransfer.ErrEditTooLarge):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": err.Error(), "code": "too_large"})
	case errors.Is(err, filetransfer.ErrEditNotText), errors.Is(err, filetransfer.ErrEditDirectory):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "code": "not_text"})
	default:
		writeError(w, http.StatusBadGateway, err.Error())
	}
}

func (s *Server) startFileTransfer(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	var input struct {
		Direction   string `json:"direction"`
		LocalPath   string `json:"local_path"`
		RemotePath  string `json:"remote_path"`
		Overwrite   bool   `json:"overwrite"`
		IsDirectory bool   `json:"is_directory"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	task, err := s.app.Files.StartTransfer(r.PathValue("id"), input.Direction, input.LocalPath, input.RemotePath, input.Overwrite, input.IsDirectory, s.app.LocalFiles)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task": task})
}

func (s *Server) listFileTransfers(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"transfers": s.app.Files.Tasks()})
}

func (s *Server) clearFinishedFileTransfers(w http.ResponseWriter, _ *http.Request) {
	removed := s.app.Files.ClearFinishedTasks()
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

func (s *Server) getFileTransfer(w http.ResponseWriter, r *http.Request) {
	task, err := s.app.Files.Task(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"task": task})
}

func (s *Server) cancelFileTransfer(w http.ResponseWriter, r *http.Request) {
	if err := s.app.Files.CancelTask(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) resumeFileTransfer(w http.ResponseWriter, r *http.Request) {
	task, err := s.app.Files.ResumeTask(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task": task})
}

func (s *Server) restartFileTransfer(w http.ResponseWriter, r *http.Request) {
	task, err := s.app.Files.RestartTask(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task": task})
}
