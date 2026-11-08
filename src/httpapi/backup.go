package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/filetransfer"
)

// Backup and restore of the receiver's channel list (/etc/enigma2).
//
// A "channels" backup holds only what makes up the channel list. A restore
// only ever writes those same files: files such as "settings" are rewritten by
// Enigma2 when it shuts down, so putting them back on a running receiver
// would be undone, or leave it in a mixed state.

const (
	backupMaxFile  = 24 << 20  // one file
	backupMaxTotal = 128 << 20 // everything read for one backup
	restoreMaxZip  = 64 << 20  // an uploaded backup
	restoreMaxData = 128 << 20 // unpacked size, a guard against zip bombs
	restoreMaxFile = 24 << 20
	restoreMaxList = 3000 // entries in an uploaded backup
	backupManifest = "dcclite-backup.json"
)

var backupFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

// isChannelFile reports whether a file in /etc/enigma2 belongs to the channel list.
func isChannelFile(name string) bool {
	if !backupFileName.MatchString(name) {
		return false
	}
	switch name {
	case "lamedb", "lamedb5", "bouquets.tv", "bouquets.radio", "blacklist", "whitelist", "whitelist_streamrelay":
		return true
	}
	for _, prefix := range []string{"userbouquet.", "alternatives."} {
		if strings.HasPrefix(name, prefix) && (strings.HasSuffix(name, ".tv") || strings.HasSuffix(name, ".radio")) {
			return true
		}
	}
	return false
}

// restoreRank orders a restore so the bouquet index files, which refer to the
// others, are written last and never point at a file that is not there yet.
func restoreRank(name string) int {
	switch {
	case name == "lamedb" || name == "lamedb5":
		return 0
	case name == "bouquets.tv" || name == "bouquets.radio":
		return 3
	case strings.HasPrefix(name, "userbouquet.") || strings.HasPrefix(name, "alternatives."):
		return 1
	}
	return 2
}

func (s *Server) sessionProfile(sessionID string) (config.Profile, bool) {
	for _, info := range s.app.Files.Sessions() {
		if info.ID == sessionID {
			return s.app.Profiles.Get(info.ProfileID)
		}
	}
	return config.Profile{}, false
}

func backupHostLabel(host string) string {
	var b strings.Builder
	for _, r := range host {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "receiver"
	}
	return b.String()
}

// backupEnigma2 streams a zip of the channel list (scope=channels) or of every
// plain file of the settings folder (scope=all).
func (s *Server) backupEnigma2(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	profile, ok := s.sessionProfile(id)
	if !ok {
		writeError(w, http.StatusNotFound, "file session not found")
		return
	}
	dir, err := bouquetDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	scope := r.URL.Query().Get("scope")
	if scope != "all" {
		scope = "channels"
	}
	keep := isChannelFile
	if scope == "all" {
		keep = backupFileName.MatchString
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	files, skipped, err := s.app.Files.ReadDirFiles(ctx, id, dir, keep, backupMaxFile, backupMaxTotal)
	if err != nil {
		writeEditError(w, err)
		return
	}
	if len(files) == 0 {
		writeError(w, http.StatusNotFound, "no channel list files were found in "+dir)
		return
	}
	now := time.Now()
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	manifest, _ := json.MarshalIndent(map[string]any{
		"application": "DCC Lite", "created": now.Format(time.RFC3339), "scope": scope,
		"source_host": profile.Host, "folder": dir, "files": names, "skipped_too_large": skipped,
	}, "", "  ")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) error {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err != nil {
			return err
		}
		_, err = fw.Write(data)
		return err
	}
	if err := add(backupManifest, manifest); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, f := range files {
		if err := add(f.Name, f.Data); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	if err := zw.Close(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	label := "enigma2-" + scope + "-" + backupHostLabel(profile.Host) + "-" + now.Format("20060102-1504") + ".zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+label+`"`)
	w.Header().Set("Content-Length", fmt.Sprint(buf.Len()))
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

type restoreSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

// restoreEnigma2 puts the channel list files of an uploaded backup back on the receiver.
func (s *Server) restoreEnigma2(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	profile, ok := s.sessionProfile(id)
	if !ok {
		writeError(w, http.StatusNotFound, "file session not found")
		return
	}
	dir, err := bouquetDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	longTransfer(w)
	r.Body = http.MaxBytesReader(w, r.Body, restoreMaxZip+(1<<20))
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, http.StatusBadRequest, "send the backup as a multipart upload")
		return
	}
	var upload []byte
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "the backup is too large or the upload was interrupted")
			return
		}
		if part.FormName() != "file" {
			continue
		}
		upload, err = io.ReadAll(io.LimitReader(part, restoreMaxZip+1))
		if err != nil || len(upload) > restoreMaxZip {
			writeError(w, http.StatusRequestEntityTooLarge, "the backup is larger than 64 MB")
			return
		}
	}
	files, skipped, err := readRestoreZip(upload)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	sort.SliceStable(files, func(i, j int) bool { return restoreRank(files[i].Name) < restoreRank(files[j].Name) })
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	restored := []string{}
	for _, f := range files {
		if err := s.app.Files.PutFile(ctx, id, dir+"/"+f.Name, f.Data); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error":    fmt.Sprintf("Restoring %s failed: %v", f.Name, err),
				"code":     "restore_failed",
				"restored": restored,
			})
			return
		}
		restored = append(restored, f.Name)
	}
	reloaded, note := reloadChannelLists(ctx, profile)
	s.names.mu.Lock()
	s.names.entries = nil // channel names come from the restored lamedb now
	s.names.mu.Unlock()
	piconCache.forget()
	serviceIndex.forget()
	writeJSON(w, http.StatusOK, map[string]any{"restored": restored, "skipped": skipped, "reloaded": reloaded, "reload_note": note})
}

// readRestoreZip validates an uploaded backup and returns the channel list files in it.
func readRestoreZip(upload []byte) ([]filetransfer.RemoteFile, []restoreSkip, error) {
	if len(upload) == 0 {
		return nil, nil, errors.New("choose a backup .zip file")
	}
	zr, err := zip.NewReader(bytes.NewReader(upload), int64(len(upload)))
	if err != nil {
		return nil, nil, errors.New("this is not a valid .zip file")
	}
	if len(zr.File) > restoreMaxList {
		return nil, nil, errors.New("the backup holds too many files")
	}
	var files []filetransfer.RemoteFile
	var skipped []restoreSkip
	var total int64
	seen := map[string]bool{}
	hasList := false
	for _, f := range zr.File {
		name := f.Name
		switch {
		case f.FileInfo().IsDir() || name == backupManifest:
			continue
		case !isChannelFile(name):
			skipped = append(skipped, restoreSkip{Name: name, Reason: "not a channel list file"})
			continue
		case seen[name]:
			skipped = append(skipped, restoreSkip{Name: name, Reason: "listed twice"})
			continue
		case f.UncompressedSize64 > restoreMaxFile:
			skipped = append(skipped, restoreSkip{Name: name, Reason: "file is too large"})
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, nil, fmt.Errorf("cannot read %s from the backup", name)
		}
		data, err := io.ReadAll(io.LimitReader(rc, restoreMaxFile+1))
		rc.Close()
		if err != nil || len(data) > restoreMaxFile {
			return nil, nil, fmt.Errorf("%s in the backup is damaged or too large", name)
		}
		total += int64(len(data))
		if total > restoreMaxData {
			return nil, nil, errors.New("the backup holds too much data")
		}
		seen[name] = true
		if name == "lamedb" || name == "lamedb5" || name == "bouquets.tv" || name == "bouquets.radio" {
			hasList = true
		}
		files = append(files, filetransfer.RemoteFile{Name: name, Data: data})
	}
	if !hasList {
		return nil, nil, errors.New("this does not look like a channel list backup (no lamedb or bouquets file)")
	}
	return files, skipped, nil
}

// reloadChannelLists asks the receiver's web interface to re-read lamedb and
// the bouquets (mode 0 = both). A failure is not fatal: the files are in place.
func reloadChannelLists(ctx context.Context, profile config.Profile) (bool, string) {
	web := newDeviceWeb(profile, 10*time.Second)
	rctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := web.get(rctx, "/web/servicelistreload?mode=0")
	if err != nil {
		return false, web.unreachable(err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("The web interface answered HTTP %d.", resp.StatusCode)
	}
	return true, ""
}
