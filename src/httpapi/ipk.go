package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"time"
)

const ipkMaxBytes = 256 << 20

var ipkFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,150}\.ipk$`)

// uploadIPK puts an .ipk package from the browser into /tmp on the receiver and
// returns the command that installs it. The page then runs that command in a
// terminal tab, so the whole opkg output is visible.
func (s *Server) uploadIPK(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.sessionProfile(id); !ok {
		writeError(w, http.StatusNotFound, "file session not found")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, ipkMaxBytes+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "send the package as a multipart upload")
		return
	}
	var tmpPath, name string
	defer func() {
		if tmpPath != "" {
			_ = os.Remove(tmpPath)
		}
	}()
	for tmpPath == "" {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "the package is too large or the upload was interrupted")
			return
		}
		if part.FormName() != "file" {
			continue
		}
		name = strings.ReplaceAll(part.FileName(), " ", "_")
		if !ipkFileName.MatchString(name) {
			writeError(w, http.StatusUnprocessableEntity, "choose an .ipk file whose name has only letters, digits and . _ + -")
			return
		}
		tmp, err := os.CreateTemp("", "dcclite-ipk-*")
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		tmpPath = tmp.Name()
		n, copyErr := io.Copy(tmp, io.LimitReader(part, ipkMaxBytes+1))
		closeErr := tmp.Close()
		if copyErr != nil || closeErr != nil {
			writeError(w, http.StatusBadGateway, "the upload was interrupted")
			return
		}
		if n > ipkMaxBytes {
			writeError(w, http.StatusRequestEntityTooLarge, "the package is larger than 256 MB")
			return
		}
		if n == 0 {
			writeError(w, http.StatusUnprocessableEntity, "the file is empty")
			return
		}
	}
	if tmpPath == "" {
		writeError(w, http.StatusBadRequest, "choose an .ipk file")
		return
	}
	remote := "/tmp/" + name
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Minute)
	defer cancel()
	if err := s.app.Files.UploadFile(ctx, id, tmpPath, remote); err != nil {
		writeEditError(w, err)
		return
	}
	// Keep the package after a failed install so it can be retried; remove it after a good one.
	command := "opkg install " + remote + " && rm -f " + remote
	writeJSON(w, http.StatusOK, map[string]any{"path": remote, "command": command})
}
