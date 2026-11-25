package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
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

const (
	backupMaxFile  = 24 << 20
	backupMaxTotal = 128 << 20
	restoreMaxZip  = 64 << 20
	restoreMaxData = 128 << 20
	restoreMaxFile = 24 << 20
	restoreMaxList = 3000
	backupManifest = "dcclite-backup.json"
)

var backupFileName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._+-]{0,127}$`)

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

type restoreSkip struct {
	Name   string `json:"name"`
	Reason string `json:"reason"`
}

func (s *Server) restoreEnigma2(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "Enigma2 restore requested")
	id := r.PathValue("id")
	profile, ok := s.sessionProfile(id)
	if !ok {
		writeSessionNotFound(w)
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
		writeError(w, http.StatusBadRequest, "Upload the backup as a file.")
		return
	}
	var upload []byte
	for {
		part, err := reader.NextPart()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			writeError(w, http.StatusRequestEntityTooLarge, "The upload failed or is too large.")
			return
		}
		if part.FormName() != "file" {
			continue
		}
		upload, err = io.ReadAll(io.LimitReader(part, restoreMaxZip+1))
		if err != nil || len(upload) > restoreMaxZip {
			writeError(w, http.StatusRequestEntityTooLarge, "The backup is larger than 64 MB.")
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
	if err := s.snapshotChannelsChecked(ctx, id, profile, dir, "before-restore", true); err != nil {
		writeJSON(w, http.StatusConflict, map[string]any{
			"error": "Nothing was changed. The current channel list could not be saved first: " + err.Error(),
			"code":  "restore_point_failed",
		})
		return
	}
	s.applyChannelFiles(ctx, w, id, profile, dir, files, skipped)
}

func (s *Server) applyChannelFiles(ctx context.Context, w http.ResponseWriter, id string, profile config.Profile, dir string, files []filetransfer.RemoteFile, skipped []restoreSkip) {
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
	s.names.entries = nil
	s.names.mu.Unlock()
	piconCache.forget()
	serviceIndex.forget()
	writeJSON(w, http.StatusOK, map[string]any{"restored": restored, "skipped": skipped, "reloaded": reloaded, "reload_note": note})
}

func readRestoreZip(upload []byte) ([]filetransfer.RemoteFile, []restoreSkip, error) {
	if len(upload) == 0 {
		return nil, nil, errors.New("Choose a backup file.")
	}
	zr, err := zip.NewReader(bytes.NewReader(upload), int64(len(upload)))
	if err != nil {
		return nil, nil, errors.New("This is not a valid .zip file.")
	}
	if len(zr.File) > restoreMaxList {
		return nil, nil, errors.New("The backup has too many files.")
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
			return nil, nil, fmt.Errorf("Cannot read %s from the backup.", name)
		}
		data, err := io.ReadAll(io.LimitReader(rc, restoreMaxFile+1))
		rc.Close()
		if err != nil || len(data) > restoreMaxFile {
			return nil, nil, fmt.Errorf("%s in the backup is damaged or too large", name)
		}
		total += int64(len(data))
		if total > restoreMaxData {
			return nil, nil, errors.New("The backup is too large.")
		}
		seen[name] = true
		if name == "lamedb" || name == "lamedb5" || name == "bouquets.tv" || name == "bouquets.radio" {
			hasList = true
		}
		files = append(files, filetransfer.RemoteFile{Name: name, Data: data})
	}
	if !hasList {
		return nil, nil, errors.New("No channel list found (lamedb or bouquets).")
	}
	return files, skipped, nil
}

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
