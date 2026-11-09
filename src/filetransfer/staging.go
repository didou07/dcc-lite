package filetransfer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	stagedKeepFailed   = 6 * time.Hour
	stagedKeepDownload = 45 * time.Minute
	stagedKeepIdle     = 15 * time.Minute
)

var ErrStagedNotReady = errors.New("the file is not ready yet")

func stagedID(localPath string) string {
	id, _, _ := strings.Cut(filepath.ToSlash(localPath), "/")
	return id
}

func (m *Manager) StagedDownload(taskID string) (string, TaskInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.tasks[taskID]
	if !ok || !entry.info.Staged || entry.info.Direction != "download" {
		return "", TaskInfo{}, ErrTaskNotFound
	}
	if entry.info.State != "completed" {
		return "", entry.info, ErrStagedNotReady
	}
	return entry.localAbsolute, entry.info, nil
}

func removeStagedFiles(entry *taskEntry) {
	if entry == nil || !entry.info.Staged || entry.local == nil || entry.localAbsolute == "" {
		return
	}
	dir := filepath.Dir(entry.localAbsolute)

	if filepath.Dir(dir) != entry.local.Root() || filepath.Base(dir) == "." || filepath.Base(dir) == string(filepath.Separator) {
		return
	}
	_ = os.RemoveAll(dir)
}

func (m *Manager) ReleaseStaged(taskID string) {
	m.mu.Lock()
	entry := m.tasks[taskID]
	m.mu.Unlock()
	if entry != nil && entry.info.Staged && entry.info.Direction == "download" && entry.info.State == "completed" {
		removeStagedFiles(entry)
	}
}

func (m *Manager) SweepStaged(staging *LocalFS, now time.Time) {
	if staging == nil {
		return
	}
	keep := make(map[string]bool)
	m.mu.Lock()
	for _, entry := range m.tasks {
		if !entry.info.Staged {
			continue
		}
		id := stagedID(entry.info.LocalPath)
		age := now.Sub(entry.info.UpdatedAt)
		switch entry.info.State {
		case "queued", "running":
			keep[id] = true
		case "failed", "cancelled":
			if age < stagedKeepFailed {
				keep[id] = true
			}
		case "completed":
			if entry.info.Direction == "download" && age < stagedKeepDownload {
				keep[id] = true
			}
		}
	}
	m.mu.Unlock()

	children, err := os.ReadDir(staging.Root())
	if err != nil {
		return
	}
	for _, child := range children {
		if keep[child.Name()] {
			continue
		}
		dir := filepath.Join(staging.Root(), child.Name())
		if newestWrite(dir).After(now.Add(-stagedKeepIdle)) {
			continue
		}
		_ = os.RemoveAll(dir)
	}
}

func newestWrite(dir string) time.Time {
	newest := time.Time{}
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if info, infoErr := d.Info(); infoErr == nil && info.ModTime().After(newest) {
			newest = info.ModTime()
		}
		return nil
	})
	return newest
}
