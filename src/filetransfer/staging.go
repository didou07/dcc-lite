package filetransfer

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Staging carries files between the user's browser and the receiver. A remote
// user works from another computer, so "my files" are the ones on that
// computer, not the ones on the machine running DCC Lite. An upload is first
// received into a private staging folder and then sent to the receiver as an
// ordinary queued transfer (progress, cancel, resume); a download is first
// fetched from the receiver into the staging folder and then handed to the
// browser. Each file lives in its own sub-folder: <staging>/<random id>/<name>.

const (
	// stagedKeepFailed is how long the staged bytes of a failed or cancelled
	// transfer stay around so that it can still be resumed.
	stagedKeepFailed = 6 * time.Hour
	// stagedKeepDownload is how long a finished download waits for the browser.
	stagedKeepDownload = 45 * time.Minute
	// stagedKeepIdle is how long an unreferenced staging folder survives after
	// its newest write (a browser upload that is still arriving keeps writing).
	stagedKeepIdle = 15 * time.Minute
)

// ErrStagedNotReady is returned when a staged download was not finished yet.
var ErrStagedNotReady = errors.New("the file is not ready yet")

func stagedID(localPath string) string {
	id, _, _ := strings.Cut(filepath.ToSlash(localPath), "/")
	return id
}

// StagedDownload returns the absolute path of a finished staged download.
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

// removeStagedFiles deletes the sub-folder that holds a staged transfer.
func removeStagedFiles(entry *taskEntry) {
	if entry == nil || !entry.info.Staged || entry.local == nil || entry.localAbsolute == "" {
		return
	}
	dir := filepath.Dir(entry.localAbsolute)
	// Only ever remove a direct child of the staging root.
	if filepath.Dir(dir) != entry.local.Root() || filepath.Base(dir) == "." || filepath.Base(dir) == string(filepath.Separator) {
		return
	}
	_ = os.RemoveAll(dir)
}

// ReleaseStaged forgets the staged copy of a download once the browser took it.
func (m *Manager) ReleaseStaged(taskID string) {
	m.mu.Lock()
	entry := m.tasks[taskID]
	m.mu.Unlock()
	if entry != nil && entry.info.Staged && entry.info.Direction == "download" && entry.info.State == "completed" {
		removeStagedFiles(entry)
	}
}

// SweepStaged deletes staging folders nothing needs any more: finished or
// abandoned uploads and downloads, and leftovers of a previous run.
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
