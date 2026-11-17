package httpapi

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/logx"
)

var snapLog = logx.For("restore-points")

const (
	restorePointKeep     = 15
	restorePointMinGap   = 10 * time.Minute // edits within this gap share the point taken at the start
	restorePointTimeout  = 90 * time.Second
	restorePointFileSize = 24 << 20
)

var restorePointName = regexp.MustCompile(`^[0-9]{8}-[0-9]{6}-[a-z-]{1,24}\.zip$`)

type snapshotState struct {
	mu   sync.Mutex
	last map[string]time.Time // device id -> time of the latest point
}

func (s *Server) restorePointDir(profileID string) string {
	if s.app.DataDir == "" || profileID == "" {
		return ""
	}
	return filepath.Join(s.app.DataDir, "restore-points", profileID)
}

// autoSnapshot is called before an edit. It never blocks or fails the edit.
func (s *Server) autoSnapshot(ctx context.Context, sessionID, dir, reason string) {
	profile, ok := s.sessionProfile(sessionID)
	if !ok {
		return
	}
	s.snapshotChannels(ctx, sessionID, profile, dir, reason, false)
}

func (s *Server) snapshotChannels(ctx context.Context, sessionID string, profile config.Profile, dir, reason string, force bool) {
	folder := s.restorePointDir(profile.ID)
	if folder == "" {
		return
	}
	s.snaps.mu.Lock()
	defer s.snaps.mu.Unlock()
	if s.snaps.last == nil {
		s.snaps.last = map[string]time.Time{}
	}
	if !force && time.Since(s.snaps.last[profile.ID]) < restorePointMinGap {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), restorePointTimeout)
	defer cancel()
	files, _, err := s.app.Files.ReadDirFiles(ctx, sessionID, dir, isChannelFile, restorePointFileSize, backupMaxTotal)
	if err != nil || len(files) == 0 {
		if err != nil {
			snapLog.Warn("could not take a restore point", "device", profile.Name, "error", err)
		}
		return
	}
	now := time.Now()
	names := make([]string, 0, len(files))
	for _, f := range files {
		names = append(names, f.Name)
	}
	manifest, _ := json.MarshalIndent(map[string]any{
		"application": "DCC Lite", "created": now.Format(time.RFC3339), "scope": "channels", "reason": reason,
		"source_host": profile.Host, "folder": dir, "files": names,
	}, "", "  ")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	add := func(name string, data []byte) error {
		fw, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Deflate, Modified: now})
		if err == nil {
			_, err = fw.Write(data)
		}
		return err
	}
	if add(backupManifest, manifest) != nil {
		return
	}
	for _, f := range files {
		if add(f.Name, f.Data) != nil {
			return
		}
	}
	if zw.Close() != nil {
		return
	}
	if err := os.MkdirAll(folder, 0o700); err != nil {
		snapLog.Warn("could not create the restore point folder", "error", err)
		return
	}
	file := filepath.Join(folder, now.Format("20060102-150405")+"-"+reason+".zip")
	if err := os.WriteFile(file, buf.Bytes(), 0o600); err != nil {
		snapLog.Warn("could not save a restore point", "error", err)
		return
	}
	s.snaps.last[profile.ID] = now
	pruneRestorePoints(folder)
	snapLog.Info("restore point saved", "device", profile.Name, "reason", reason, "files", len(files))
}

func pruneRestorePoints(folder string) {
	entries, err := os.ReadDir(folder)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && restorePointName.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names) // the name starts with the time, so oldest first
	for len(names) > restorePointKeep {
		_ = os.Remove(filepath.Join(folder, names[0]))
		names = names[1:]
	}
}

type restorePointInfo struct {
	Name    string    `json:"name"`
	Reason  string    `json:"reason"`
	Created time.Time `json:"created"`
	Size    int64     `json:"size"`
}

func (s *Server) listRestorePoints(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.sessionProfile(r.PathValue("id"))
	if !ok {
		writeSessionNotFound(w)
		return
	}
	out := []restorePointInfo{}
	if folder := s.restorePointDir(profile.ID); folder != "" {
		entries, _ := os.ReadDir(folder)
		for _, e := range entries {
			if e.IsDir() || !restorePointName.MatchString(e.Name()) {
				continue
			}
			info, err := e.Info()
			if err != nil {
				continue
			}
			created, err := time.ParseInLocation("20060102-150405", e.Name()[:15], time.Local)
			if err != nil {
				continue
			}
			out = append(out, restorePointInfo{Name: e.Name(), Reason: e.Name()[16 : len(e.Name())-4], Created: created.UTC(), Size: info.Size()})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"points": out, "keep": restorePointKeep})
}

func (s *Server) restoreFromPoint(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "restore point restore requested")
	id := r.PathValue("id")
	profile, ok := s.sessionProfile(id)
	if !ok {
		writeSessionNotFound(w)
		return
	}
	name := r.PathValue("name")
	folder := s.restorePointDir(profile.ID)
	if folder == "" || !restorePointName.MatchString(name) {
		writeError(w, http.StatusNotFound, "restore point not found")
		return
	}
	dir, err := bouquetDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	f, err := os.Open(filepath.Join(folder, name))
	if err != nil {
		writeError(w, http.StatusNotFound, "restore point not found")
		return
	}
	data, err := io.ReadAll(io.LimitReader(f, restoreMaxZip+1))
	f.Close()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "the restore point could not be read")
		return
	}
	files, skipped, err := readRestoreZip(data)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	sort.SliceStable(files, func(i, j int) bool { return restoreRank(files[i].Name) < restoreRank(files[j].Name) })
	longTransfer(w)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Minute)
	defer cancel()
	s.snapshotChannels(ctx, id, profile, dir, "before-restore", true)
	s.applyChannelFiles(ctx, w, id, profile, dir, files, skipped)
}

func (s *Server) deleteRestorePoint(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.sessionProfile(r.PathValue("id"))
	if !ok {
		writeSessionNotFound(w)
		return
	}
	name := r.PathValue("name")
	folder := s.restorePointDir(profile.ID)
	if folder == "" || !restorePointName.MatchString(name) {
		writeError(w, http.StatusNotFound, "restore point not found")
		return
	}
	if err := os.Remove(filepath.Join(folder, name)); err != nil {
		writeError(w, http.StatusNotFound, "restore point not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
