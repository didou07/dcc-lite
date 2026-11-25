package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/filetransfer"
)

func (s *Server) settingsBackupDir(profileID string) string {
	if s.app.DataDir == "" || profileID == "" {
		return ""
	}
	return filepath.Join(s.app.DataDir, "settings-backups", profileID)
}

func readSettingsUpload(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, settingsMaxArchive+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "Upload the backup as a file.")
		return nil, false
	}
	var upload []byte
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "The upload failed or is too large.")
			return nil, false
		}
		if part.FormName() != "file" {
			continue
		}
		upload, err = io.ReadAll(io.LimitReader(part, settingsMaxArchive+1))
		if err != nil || len(upload) > settingsMaxArchive {
			writeError(w, http.StatusRequestEntityTooLarge, "The backup is larger than 64 MB.")
			return nil, false
		}
	}
	return upload, true
}

func (s *Server) settingsRestorePreview(w http.ResponseWriter, r *http.Request) {
	if s.denyRemoteBackup(w) {
		return
	}
	if _, ok := s.enabledProfile(w, r); !ok {
		return
	}
	upload, ok := readSettingsUpload(w, r)
	if !ok {
		return
	}
	upload, skipped, err := prepareSettingsUpload(upload)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	rev, err := reviewSettingsArchive(upload)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"scopes": rev.Scopes, "files": len(rev.Entries), "bytes": rev.Bytes, "skipped": skipped})
}

func (s *Server) settingsRestore(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "settings restore requested")
	if s.denyRemoteBackup(w) {
		return
	}
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	after := r.URL.Query().Get("after")
	if after != "reboot" {
		after = "restart"
	}
	longTransfer(w)
	upload, ok := readSettingsUpload(w, r)
	if !ok {
		return
	}
	upload, skipped, err := prepareSettingsUpload(upload)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	rev, err := reviewSettingsArchive(upload)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if _, busy := backupBusy.LoadOrStore("set-"+profile.ID, true); busy {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Another backup or restore is running.", "code": "busy"})
		return
	}
	defer backupBusy.Delete("set-" + profile.ID)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()

	var scopes []string
	for id := range rev.Scopes {
		scopes = append(scopes, id)
	}
	folder := s.settingsBackupDir(profile.ID)
	if folder == "" {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Nothing was changed: no folder for the safety copy.", "code": "no_safety_copy"})
		return
	}
	current, err := s.fetchSettingsArchive(ctx, profile, scopes)
	if errors.Is(err, errNoSettingsFiles) {
		current, err = nil, nil
	}
	if err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{"error": "Nothing was changed. The current files could not be saved first: " + err.Error(), "code": "safety_copy_failed"})
		return
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create the safety-copy folder.")
		return
	}
	safety := ""
	if len(current) > 0 {
		safety = filepath.Join(folder, "before-restore-"+time.Now().Format("20060102-150405")+".tar.gz")
		if err := os.WriteFile(safety, current, 0o600); err != nil {
			writeError(w, http.StatusInternalServerError, "Could not write the safety copy.")
			return
		}
		pruneSettingsSafetyCopies(folder)
	}

	// 2. Upload and verify.
	job, err := newSettingsJobID()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	remote := "/tmp/dcc-settings-" + strings.TrimPrefix(job, "set-") + ".tar.gz"
	tmp, err := os.CreateTemp("", "dcclite-restore-*.tar.gz")
	if err != nil {
		writeError(w, http.StatusInternalServerError, "Could not create a temporary file.")
		return
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(upload); err != nil {
		tmp.Close()
		writeError(w, http.StatusInternalServerError, "Could not write a temporary file.")
		return
	}
	tmp.Close()
	sum, _, _ := fileMD5(tmp.Name())
	ftp, err := filetransfer.OpenFTP(ctx, filetransfer.FTPOptions{Host: profile.Host, Port: profile.EffectiveFTPPort(), Username: telnetUser(profile), Password: profile.Password})
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "Nothing was changed. FTP failed: " + cleanText(err.Error(), 120), "code": "ftp_failed"})
		return
	}
	defer ftp.Close()
	if err := ftp.Upload(ctx, tmp.Name(), remote, nil); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "Nothing was changed. Upload failed: " + cleanText(err.Error(), 120), "code": "ftp_failed"})
		return
	}
	run := shellRunner{profile}
	out, _, err := run.run(ctx, time.Minute, "md5sum "+remote+"; tar -tzf "+remote+" >/dev/null 2>&1 && echo TAROK", 8<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	if !strings.Contains(out, sum) || !strings.Contains(out, "TAROK") {
		_, _, _ = run.run(ctx, 15*time.Second, "rm -f "+remote, 2<<10)
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "Nothing was changed. The file arrived damaged.", "code": "checksum_mismatch"})
		return
	}

	finish := "(systemctl start enigma2 2>/dev/null || init 3)"
	if after == "reboot" {
		finish = "reboot"
	}
	body := "sleep 2; (systemctl stop enigma2 2>/dev/null || init 4); sleep 4; tar -xzf " + remote + " -C / ; rm -f " + remote + "; sync; " + finish
	cmd := "nohup sh -c '" + body + "' > /dev/null 2>&1 & echo started"
	if _, _, err := run.run(ctx, 30*time.Second, cmd, 2<<10); err != nil {
		telnetFailure(w, profile, err)
		return
	}
	s.names.mu.Lock()
	s.names.entries = nil
	s.names.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"restored": len(rev.Entries), "scopes": rev.Scopes, "after": after,
		"safety_copy": safetyName(safety), "safety_dir": folder, "skipped": skipped, "note": "Applying now. Enigma2 is down for about a minute.",
	})
}

func safetyName(p string) string {
	if p == "" {
		return ""
	}
	return filepath.Base(p)
}

func pruneSettingsSafetyCopies(folder string) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && settingsSafetyRe.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > settingsSafetyKeep {
		_ = os.Remove(filepath.Join(folder, names[0]))
		names = names[1:]
	}
}
