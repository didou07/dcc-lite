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

var ipkFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+~-]{0,150}\.ipk$`)

func (s *Server) uploadIPK(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "plugin install (.ipk) requested")
	id := r.PathValue("id")
	if _, ok := s.sessionProfile(id); !ok {
		writeError(w, http.StatusNotFound, "file session not found")
		return
	}
	longTransfer(w)
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
			writeError(w, http.StatusUnprocessableEntity, "choose an .ipk file whose name has only letters, digits and . _ + ~ -")
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

	install := "opkg install "
	if r.URL.Query().Get("force") == "1" {
		install += "--force-reinstall --force-overwrite "
	}
	command := install + remote + " && rm -f " + remote
	if r.URL.Query().Get("restart") == "1" {
		command += " && sleep 1 && init 4 && sleep 3 && init 3"
	}
	writeJSON(w, http.StatusOK, map[string]any{"path": remote, "command": command})
}
