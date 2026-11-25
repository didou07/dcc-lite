package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
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
		"error": "This computer's files are not available in a remote session.",
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
	writeJSON(w, http.StatusOK, map[string]any{"root": s.app.LocalFiles.RootLabel(), "path": current, "parent": s.app.LocalFiles.ParentOf(current), "entries": entries})
}

func (s *Server) listLocalPlaces(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	if s.app.LocalFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "local file browser is unavailable")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"places": s.app.LocalFiles.Places()})
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

func (s *Server) copyLocalFile(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	logAction(w, r, "local file copy requested")
	var input struct {
		SrcPath   string `json:"src_path"`
		DstPath   string `json:"dst_path"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if s.app.LocalFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "local file browser is unavailable")
		return
	}
	if err := s.app.LocalFiles.Copy(r.Context(), input.SrcPath, input.DstPath, input.Overwrite); err != nil {
		writeCopyError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) readLocalText(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	localPath := r.URL.Query().Get("path")
	if localPath == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if s.app.LocalFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "local file browser is unavailable")
		return
	}
	file, err := s.app.LocalFiles.ReadText(localPath)
	if err != nil {
		writeEditError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": file, "max_bytes": filetransfer.MaxEditBytes})
}

func (s *Server) writeLocalText(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	logAction(w, r, "local file save requested")
	var input struct {
		Path               string    `json:"path"`
		Content            string    `json:"content"`
		LineEnding         string    `json:"line_ending"`
		Encoding           string    `json:"encoding"`
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
	if s.app.LocalFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "local file browser is unavailable")
		return
	}
	file, err := s.app.LocalFiles.WriteText(input.Path, filetransfer.TextWrite{
		Content: input.Content, LineEnding: input.LineEnding, Encoding: input.Encoding,
		ExpectedSize: input.ExpectedSize, ExpectedModifiedAt: input.ExpectedModifiedAt, ExpectedHash: input.ExpectedHash,
		Force: input.Force, Create: input.Create,
	})
	if err != nil {
		writeEditError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": file})
}

func writeCopyError(w http.ResponseWriter, err error) {
	if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "already contains") || strings.Contains(err.Error(), "different file type") {
		writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "exists"})
		return
	}
	writeError(w, http.StatusUnprocessableEntity, err.Error())
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
		writeFilesError(w, err)
		return
	}
	parent := path.Dir(remotePath)
	if remotePath == "/" {
		parent = "/"
	} else if remotePath == "." {
		parent = "."
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
		writeFilesError(w, err)
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
		writeFilesError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeRemoteFile(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "remote file delete requested")
	var input struct {
		Path      string `json:"path"`
		Directory bool   `json:"directory"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.app.Files.Remove(r.Context(), r.PathValue("id"), input.Path, input.Directory); err != nil {
		writeFilesError(w, err)
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
		writeFilesError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) copyRemoteFile(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "remote file copy requested")
	var input struct {
		SrcPath   string `json:"src_path"`
		DstPath   string `json:"dst_path"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.app.Files.Copy(r.Context(), r.PathValue("id"), input.SrcPath, input.DstPath, input.Overwrite); err != nil {
		if strings.Contains(err.Error(), "already exists") || strings.Contains(err.Error(), "already contains") || strings.Contains(err.Error(), "different file type") {
			writeJSON(w, http.StatusConflict, map[string]any{"error": err.Error(), "code": "exists"})
			return
		}
		writeFilesError(w, err)
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
	logAction(w, r, "remote file save requested")
	var input struct {
		Path               string    `json:"path"`
		Content            string    `json:"content"`
		LineEnding         string    `json:"line_ending"`
		Encoding           string    `json:"encoding"`
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
		Content: input.Content, LineEnding: input.LineEnding, Encoding: input.Encoding,
		ExpectedSize: input.ExpectedSize, ExpectedModifiedAt: input.ExpectedModifiedAt, ExpectedHash: input.ExpectedHash,
		Force: input.Force, Create: input.Create,
	})
	if err != nil {
		writeEditError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": file})
}

func writeSessionNotFound(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "file session not found", "code": "session_not_found"})
}

func writeFilesError(w http.ResponseWriter, err error) {
	if errors.Is(err, filetransfer.ErrSessionNotFound) {
		writeSessionNotFound(w)
		return
	}
	if errors.Is(err, filetransfer.ErrPathNotFound) {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error(), "code": "path_not_found"})
		return
	}
	writeError(w, http.StatusBadGateway, err.Error())
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
	case errors.Is(err, filetransfer.ErrPathNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error(), "code": "path_not_found"})
	case errors.Is(err, filetransfer.ErrEditEncoding):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": err.Error(), "code": "encoding"})
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

func writeBytes(w http.ResponseWriter, file filetransfer.BinaryFile) {
	h := w.Header()
	h.Set("Content-Type", "application/octet-stream")
	h.Set("Content-Length", strconv.Itoa(len(file.Data)))
	h.Set("Cache-Control", "no-store")
	h.Set("X-File-Size", strconv.FormatInt(file.Size, 10))
	h.Set("X-File-Truncated", strconv.FormatBool(file.Truncated))
	h.Set("X-File-Hash", file.Hash)
	h.Set("X-File-Mode", file.Mode)
	if !file.ModifiedAt.IsZero() {
		h.Set("X-File-Modified", file.ModifiedAt.UTC().Format(time.RFC3339Nano))
	}
	if file.Link != "" {
		h.Set("X-File-Link", "1")
	}
	_, _ = w.Write(file.Data)
}

func (s *Server) readLocalBytes(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	localPath := r.URL.Query().Get("path")
	if localPath == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	if s.app.LocalFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "local file browser is unavailable")
		return
	}
	file, err := s.app.LocalFiles.ReadHead(localPath, filetransfer.MaxHexBytes)
	if err != nil {
		writeEditError(w, err)
		return
	}
	writeBytes(w, file)
}

func (s *Server) readRemoteBytes(w http.ResponseWriter, r *http.Request) {
	remotePath := r.URL.Query().Get("path")
	if remotePath == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	file, found, err := s.app.Files.ReadHead(ctx, r.PathValue("id"), remotePath, filetransfer.MaxHexBytes)
	if err != nil {
		writeEditError(w, err)
		return
	}
	if !found {
		writeEditError(w, filetransfer.ErrEditNotFound)
		return
	}
	writeBytes(w, file)
}

func binaryWriteFromRequest(w http.ResponseWriter, r *http.Request) (path string, data []byte, write filetransfer.BinaryWrite, ok bool) {
	q := r.URL.Query()
	path = q.Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return "", nil, write, false
	}
	r.Body = http.MaxBytesReader(w, r.Body, filetransfer.MaxHexBytes+1)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]any{"error": "The file is too large to save from the hex editor.", "code": "too_large"})
		return "", nil, write, false
	}
	write.ExpectedSize, _ = strconv.ParseInt(q.Get("size"), 10, 64)
	write.ExpectedModifiedAt, _ = time.Parse(time.RFC3339Nano, q.Get("modified"))
	write.ExpectedHash = q.Get("hash")
	write.Force = q.Get("force") == "1"
	if write.ExpectedHash == "" && !write.Force {
		writeError(w, http.StatusBadRequest, "the version of the file that was opened is required")
		return "", nil, write, false
	}
	return path, data, write, true
}

func (s *Server) writeLocalBytes(w http.ResponseWriter, r *http.Request) {
	if s.denyHostFiles(w) {
		return
	}
	logAction(w, r, "local hex edit save requested")
	path, data, write, ok := binaryWriteFromRequest(w, r)
	if !ok {
		return
	}
	if s.app.LocalFiles == nil {
		writeError(w, http.StatusServiceUnavailable, "local file browser is unavailable")
		return
	}
	var kept string
	write.Before = func(current []byte) (err error) {
		kept, err = s.keepOriginal("local", path, current)
		return err
	}
	file, err := s.app.LocalFiles.WriteBytes(path, data, write)
	if err != nil {
		writeEditError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": file, "original_kept": kept})
}

func (s *Server) writeRemoteBytes(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "remote hex edit save requested")
	id := r.PathValue("id")
	profile, ok := s.sessionProfile(id)
	if !ok {
		writeSessionNotFound(w)
		return
	}
	path, data, write, ok := binaryWriteFromRequest(w, r)
	if !ok {
		return
	}
	var kept string
	write.Before = func(current []byte) (err error) {
		kept, err = s.keepOriginal(profile.ID, path, current)
		return err
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	file, err := s.app.Files.WriteBytes(ctx, id, path, data, write)
	if err != nil {
		writeEditError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": file, "original_kept": kept})
}
