package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// These handlers move files between the user's browser and the receiver. A
// remote user opens DCC Lite from another computer, so the files to send live on
// that computer and the files to save must end up there: the "local" panel of
// the server (its own home folder) is of no use to them.
//
//	upload:   browser -> staging folder -> queued FTP upload (progress, cancel, resume)
//	download: queued FTP download -> staging folder -> browser (a normal file download)

const maxBrowserUpload = 8 << 30 // 8 GiB per file

// longTransfer lifts the server-wide read and write timeouts for this request.
// They are tuned for small API calls (15 s to read a request, 3 min to write a
// reply) and would cut off any big upload or download half way.
func longTransfer(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	_ = rc.SetReadDeadline(time.Time{})
	_ = rc.SetWriteDeadline(time.Time{})
}

func newStageID() (string, error) {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}

// safeStageName turns the name of a file on the user's computer into a name that
// is safe inside the staging folder and still readable in the transfer list.
func safeStageName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = path.Base(name)
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 0x20 || r == 0x7f, strings.ContainsRune(`/\:*?"<>|`, r):
			b.WriteRune('_')
		default:
			b.WriteRune(r)
		}
	}
	name = strings.Trim(b.String(), " .")
	for len(name) > 150 {
		_, size := utf8.DecodeLastRuneInString(name)
		name = name[:len(name)-size]
	}
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}

// uploadFromBrowser receives one file from the browser (the raw request body) and
// queues its upload to the receiver. Query: path = destination file on the
// receiver, overwrite = 1 to replace an existing file, mtime = the file's
// modification time in milliseconds (kept on the receiver).
func (s *Server) uploadFromBrowser(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, ok := s.sessionProfile(sessionID); !ok {
		writeError(w, http.StatusNotFound, "file session not found")
		return
	}
	if s.app.Staging == nil {
		writeError(w, http.StatusServiceUnavailable, "uploads from the browser are unavailable")
		return
	}
	query := r.URL.Query()
	remotePath := query.Get("path")
	if !strings.HasPrefix(remotePath, "/") || strings.ContainsAny(remotePath, "\r\n\x00") || path.Base(remotePath) == "/" {
		writeError(w, http.StatusBadRequest, "path must be the full destination file name on the device")
		return
	}
	longTransfer(w)
	r.Body = http.MaxBytesReader(w, r.Body, maxBrowserUpload)

	id, err := newStageID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	dir := filepath.Join(s.app.Staging.Root(), id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "could not prepare the upload: "+err.Error())
		return
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.RemoveAll(dir)
		}
	}()
	name := safeStageName(path.Base(remotePath))
	target := filepath.Join(dir, name)
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "could not prepare the upload: "+err.Error())
		return
	}
	_, copyErr := io.Copy(file, r.Body)
	closeErr := file.Close()
	if copyErr != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(copyErr, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "the file is larger than 8 GB")
			return
		}
		writeError(w, http.StatusBadRequest, "the upload was interrupted")
		return
	}
	if closeErr != nil {
		writeError(w, http.StatusInternalServerError, "could not store the upload: "+closeErr.Error())
		return
	}
	if ms, parseErr := strconv.ParseInt(query.Get("mtime"), 10, 64); parseErr == nil && ms > 0 {
		modified := time.UnixMilli(ms)
		if !modified.After(time.Now().Add(24 * time.Hour)) {
			_ = os.Chtimes(target, modified, modified)
		}
	}
	task, err := s.app.Files.StartStagedTransfer(sessionID, "upload", id+"/"+name, remotePath, query.Get("overwrite") == "1", s.app.Staging)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	cleanup = false
	writeJSON(w, http.StatusAccepted, map[string]any{"task": task})
}

// prepareBrowserDownload queues a download of one remote file into the staging
// folder. When it finishes, the page fetches it with fetchStagedDownload.
func (s *Server) prepareBrowserDownload(w http.ResponseWriter, r *http.Request) {
	sessionID := r.PathValue("id")
	if _, ok := s.sessionProfile(sessionID); !ok {
		writeError(w, http.StatusNotFound, "file session not found")
		return
	}
	if s.app.Staging == nil {
		writeError(w, http.StatusServiceUnavailable, "downloads to the browser are unavailable")
		return
	}
	var input struct {
		RemotePath string `json:"remote_path"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !strings.HasPrefix(input.RemotePath, "/") || path.Base(input.RemotePath) == "/" {
		writeError(w, http.StatusBadRequest, "remote_path must be the full path of a file on the device")
		return
	}
	id, err := newStageID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	name := safeStageName(path.Base(input.RemotePath))
	if err := os.MkdirAll(filepath.Join(s.app.Staging.Root(), id), 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "could not prepare the download: "+err.Error())
		return
	}
	task, err := s.app.Files.StartStagedTransfer(sessionID, "download", id+"/"+name, input.RemotePath, false, s.app.Staging)
	if err != nil {
		_ = os.RemoveAll(filepath.Join(s.app.Staging.Root(), id))
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"task": task})
}

type countingWriter struct {
	http.ResponseWriter
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.ResponseWriter.Write(p)
	c.n += int64(n)
	return n, err
}
func (c *countingWriter) Unwrap() http.ResponseWriter { return c.ResponseWriter }

// fetchStagedDownload hands a finished staged download to the browser as a file.
func (s *Server) fetchStagedDownload(w http.ResponseWriter, r *http.Request) {
	taskID := r.PathValue("id")
	staged, info, err := s.app.Files.StagedDownload(taskID)
	if err != nil {
		status := http.StatusNotFound
		if info.ID != "" {
			status = http.StatusConflict
		}
		writeError(w, status, err.Error())
		return
	}
	file, err := os.Open(staged)
	if err != nil {
		writeError(w, http.StatusGone, "the file is no longer available; download it again")
		return
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil || !stat.Mode().IsRegular() {
		writeError(w, http.StatusGone, "the file is no longer available; download it again")
		return
	}
	longTransfer(w)
	name := safeStageName(path.Base(info.RemotePath))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Cache-Control", "no-store")
	counter := &countingWriter{ResponseWriter: w}
	http.ServeContent(counter, r, name, stat.ModTime(), file)
	// A complete answer to a plain request means the browser has the file.
	if r.Header.Get("Range") == "" && counter.n == stat.Size() {
		s.app.Files.ReleaseStaged(taskID)
	}
}
