package filetransfer

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

type memoryTransfer struct {
	mu     sync.Mutex
	files  map[string][]byte
	dirs   map[string]bool
	modes  map[string]uint32
	closed bool
}

func newMemoryTransfer() *memoryTransfer {
	return &memoryTransfer{files: make(map[string][]byte), dirs: map[string]bool{"/": true}, modes: make(map[string]uint32)}
}

func TestEstimateTransferProgressCalculatesSmoothedSpeedAndETA(t *testing.T) {
	started := time.Unix(100, 0)
	progress := core.TransferProgress{BytesDone: 100, BytesTotal: 1100}
	progress, speed, previousBytes, previousAt := estimateTransferProgress(progress, 0, time.Time{}, 0, started)
	if progress.SpeedBytesPerSecond != 0 || previousBytes != 100 || !previousAt.Equal(started) {
		t.Fatalf("initial progress sample = %#v, speed=%v, bytes=%d, at=%v", progress, speed, previousBytes, previousAt)
	}
	next := started.Add(2 * time.Second)
	progress.BytesDone = 600
	progress, speed, previousBytes, previousAt = estimateTransferProgress(progress, previousBytes, previousAt, speed, next)
	if progress.SpeedBytesPerSecond != 250 || progress.ETASeconds != 2 || previousBytes != 600 || !previousAt.Equal(next) {
		t.Fatalf("estimated progress = %#v, speed=%v, bytes=%d, at=%v", progress, speed, previousBytes, previousAt)
	}
	completed := progress
	completed.BytesDone = completed.BytesTotal
	completed.Fraction = 1
	completed, _, _, _ = estimateTransferProgress(completed, previousBytes, previousAt, speed, next.Add(time.Second))
	if completed.ETASeconds != 0 {
		t.Fatalf("completed task retained ETA: %#v", completed)
	}
	stalled := progress
	stalled, staleSpeed, _, _ := estimateTransferProgress(stalled, previousBytes, previousAt, speed, next.Add(3*time.Second))
	if staleSpeed != 0 || stalled.SpeedBytesPerSecond != 0 || stalled.ETASeconds != 0 {
		t.Fatalf("stalled transfer retained speed/ETA: %#v speed=%v", stalled, staleSpeed)
	}
}

func TestPrunedTasksCleanOwnedPartialArtifacts(t *testing.T) {
	manager := NewManager()
	remote := newMemoryTransfer()
	remote.files["/.dccnext-stale.part"] = []byte("partial")
	session := &managedSession{
		info:    SessionInfo{ID: "session", ProfileID: "profile", Protocol: "ftp", Host: "receiver", Port: 21},
		adapter: remote,
	}
	localPartial := filepath.Join(t.TempDir(), ".partial.part")
	if err := os.WriteFile(localPartial, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := &taskEntry{
		info:          TaskInfo{ID: "stale", SessionID: "session", State: "failed", UpdatedAt: time.Now().Add(-time.Hour)},
		session:       session,
		partialPath:   localPartial,
		remotePartial: "/.dccnext-stale.part",
	}
	manager.tasks[entry.info.ID] = entry
	manager.mu.Lock()
	pruned := manager.pruneTasksLocked()
	manager.mu.Unlock()
	if len(pruned) != 1 {
		t.Fatalf("pruned %d tasks, expected 1", len(pruned))
	}
	manager.cleanupTaskArtifacts(pruned[0])
	if _, err := os.Lstat(localPartial); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("local partial still exists after pruning: %v", err)
	}
	remote.mu.Lock()
	_, exists := remote.files["/.dccnext-stale.part"]
	remote.mu.Unlock()
	if exists {
		t.Fatal("remote partial still exists after pruning")
	}
}
func (m *memoryTransfer) List(_ context.Context, dir string) ([]core.FileEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirs[dir] {
		return nil, errors.New("remote directory not found")
	}
	seen := make(map[string]core.FileEntry)
	for name := range m.dirs {
		if name == "/" || path.Dir(name) != dir {
			continue
		}
		base := path.Base(name)
		seen[base] = core.FileEntry{Name: base, Path: name, IsDir: true}
	}
	for name, data := range m.files {
		if path.Dir(name) != dir {
			continue
		}
		base := path.Base(name)
		seen[base] = core.FileEntry{Name: base, Path: name, Size: int64(len(data)), Mode: fmt.Sprintf("%04o", m.modes[name])}
	}
	out := make([]core.FileEntry, 0, len(seen))
	for _, entry := range seen {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
func (m *memoryTransfer) Upload(ctx context.Context, local, remote string, progress func(core.TransferProgress)) error {
	data, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	m.files[remote] = append([]byte(nil), data...)
	m.mu.Unlock()
	if progress != nil {
		progress(core.TransferProgress{BytesDone: int64(len(data)), BytesTotal: int64(len(data)), Fraction: 1})
	}
	return nil
}
func (m *memoryTransfer) Download(ctx context.Context, remote, local string, progress func(core.TransferProgress)) error {
	m.mu.Lock()
	data, ok := m.files[remote]
	data = append([]byte(nil), data...)
	m.mu.Unlock()
	if !ok {
		return errors.New("remote file not found")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Like the real FTP adapter, never download onto an existing file.
	if _, err := os.Stat(local); err == nil {
		return errors.New("local destination already exists")
	}
	if err := os.WriteFile(local, data, 0o600); err != nil {
		return err
	}
	if progress != nil {
		progress(core.TransferProgress{BytesDone: int64(len(data)), BytesTotal: int64(len(data)), Fraction: 1})
	}
	return nil
}
func (m *memoryTransfer) Mkdir(_ context.Context, remote string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dirs[remote] {
		return errors.New("directory already exists")
	}
	if !m.dirs[path.Dir(remote)] {
		return errors.New("parent directory missing")
	}
	m.dirs[remote] = true
	return nil
}
func (m *memoryTransfer) Rename(_ context.Context, oldPath, newPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if data, ok := m.files[oldPath]; ok {
		delete(m.files, oldPath)
		m.files[newPath] = data
		if mode, exists := m.modes[oldPath]; exists {
			delete(m.modes, oldPath)
			m.modes[newPath] = mode
		}
		return nil
	}
	if m.dirs[oldPath] {
		delete(m.dirs, oldPath)
		m.dirs[newPath] = true
		return nil
	}
	return errors.New("remote item not found")
}
func (m *memoryTransfer) Remove(_ context.Context, remote string, directory bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if directory {
		if !m.dirs[remote] {
			return errors.New("directory not found")
		}
		for name := range m.files {
			if strings.HasPrefix(name, strings.TrimSuffix(remote, "/")+"/") {
				return errors.New("directory not empty")
			}
		}
		delete(m.dirs, remote)
		return nil
	}
	if _, ok := m.files[remote]; !ok {
		return errors.New("file not found")
	}
	delete(m.files, remote)
	delete(m.modes, remote)
	return nil
}
func (m *memoryTransfer) Chmod(_ context.Context, remote string, mode uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.modes[remote] = mode
	return nil
}
func (m *memoryTransfer) Close() error { m.mu.Lock(); m.closed = true; m.mu.Unlock(); return nil }

type resumableMemoryTransfer struct {
	*memoryTransfer
	failUploadOnce   bool
	failDownloadOnce bool
}

func newResumableMemoryTransfer() *resumableMemoryTransfer {
	return &resumableMemoryTransfer{memoryTransfer: newMemoryTransfer(), failUploadOnce: true, failDownloadOnce: true}
}

func (m *resumableMemoryTransfer) DownloadPartial(ctx context.Context, remote, local string, progress func(core.TransferProgress)) error {
	m.mu.Lock()
	data, ok := m.files[remote]
	data = append([]byte(nil), data...)
	m.mu.Unlock()
	if !ok {
		return errors.New("remote file not found")
	}
	if err := os.MkdirAll(filepath.Dir(local), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(local, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if m.failDownloadOnce {
		m.failDownloadOnce = false
		cut := len(data) / 2
		if _, err := file.Write(data[:cut]); err != nil {
			return err
		}
		if progress != nil {
			progress(core.TransferProgress{BytesDone: int64(cut), BytesTotal: int64(len(data)), Fraction: fraction(int64(cut), int64(len(data)))})
		}
		return io.ErrUnexpectedEOF
	}
	_, err = file.Write(data)
	return err
}

func (m *resumableMemoryTransfer) ResumeDownload(ctx context.Context, remote, local string, progress func(core.TransferProgress)) error {
	m.mu.Lock()
	data, ok := m.files[remote]
	data = append([]byte(nil), data...)
	m.mu.Unlock()
	if !ok {
		return errors.New("remote file not found")
	}
	partial, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	if len(partial) > len(data) || !bytes.Equal(partial, data[:len(partial)]) {
		return errors.New("partial download does not match the remote file")
	}
	file, err := os.OpenFile(local, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	n, err := file.Write(data[len(partial):])
	if err == nil && progress != nil {
		done := int64(len(partial) + n)
		progress(core.TransferProgress{BytesDone: done, BytesTotal: int64(len(data)), Fraction: fraction(done, int64(len(data)))})
	}
	return err
}

func (m *resumableMemoryTransfer) Upload(ctx context.Context, local, remote string, progress func(core.TransferProgress)) error {
	data, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	if m.failUploadOnce {
		m.failUploadOnce = false
		cut := len(data) / 2
		m.mu.Lock()
		m.files[remote] = append([]byte(nil), data[:cut]...)
		m.mu.Unlock()
		return io.ErrUnexpectedEOF
	}
	return m.memoryTransfer.Upload(ctx, local, remote, progress)
}

func (m *resumableMemoryTransfer) ResumeUpload(ctx context.Context, local, remote string, progress func(core.TransferProgress)) error {
	data, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	m.mu.Lock()
	partial := append([]byte(nil), m.files[remote]...)
	m.mu.Unlock()
	if len(partial) > len(data) || !bytes.Equal(partial, data[:len(partial)]) {
		return errors.New("partial upload does not match the local source")
	}
	m.mu.Lock()
	m.files[remote] = append(partial, data[len(partial):]...)
	m.mu.Unlock()
	if progress != nil {
		size := int64(len(data))
		progress(core.TransferProgress{BytesDone: size, BytesTotal: size, Fraction: 1})
	}
	return nil
}

func waitTask(t *testing.T, manager *Manager, id string) TaskInfo {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		info, err := manager.Task(id)
		if err != nil {
			t.Fatal(err)
		}
		if info.State == "completed" || info.State == "failed" || info.State == "cancelled" {
			return info
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("transfer task did not finish")
	return TaskInfo{}
}

func TestManagerUploadsAndDownloadsDirectories(t *testing.T) {
	root := t.TempDir()
	local := &LocalFS{root: root}
	if err := os.MkdirAll(filepath.Join(root, "source", "nested"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source", "top.txt"), []byte("top-level"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "source", "nested", "child.txt"), []byte("nested-data"), 0o600); err != nil {
		t.Fatal(err)
	}

	adapter := newMemoryTransfer()
	manager := NewManager()
	manager.sessions["test-session"] = &managedSession{info: SessionInfo{ID: "test-session", ProfileID: "device"}, adapter: adapter}
	defer manager.CloseAll()

	upload, err := manager.StartTransfer("test-session", "upload", "source", "/source", false, true, local)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitTask(t, manager, upload.ID); result.State != "completed" {
		t.Fatalf("upload failed: %#v", result)
	}
	adapter.mu.Lock()
	if string(adapter.files["/source/top.txt"]) != "top-level" || string(adapter.files["/source/nested/child.txt"]) != "nested-data" {
		t.Fatalf("uploaded files missing: %#v", adapter.files)
	}
	adapter.mu.Unlock()

	download, err := manager.StartTransfer("test-session", "download", "downloaded", "/source", false, true, local)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitTask(t, manager, download.ID); result.State != "completed" {
		t.Fatalf("download failed: %#v", result)
	}
	for name, want := range map[string]string{"downloaded/top.txt": "top-level", "downloaded/nested/child.txt": "nested-data"} {
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		if err != nil || string(data) != want {
			t.Fatalf("download %s = %q, %v", name, data, err)
		}
	}
}

func TestManagerResumesInterruptedDownload(t *testing.T) {
	root := t.TempDir()
	local := &LocalFS{root: root}
	adapter := newResumableMemoryTransfer()
	adapter.files["/remote.bin"] = []byte("receiver-firmware-image")
	manager := NewManager()
	session := &managedSession{info: SessionInfo{ID: "test-session", ProfileID: "device"}, adapter: adapter}
	manager.sessions["test-session"] = session
	defer manager.CloseAll()

	task, err := manager.StartTransfer("test-session", "download", "firmware.bin", "/remote.bin", false, false, local)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitTask(t, manager, task.ID)
	if failed.State != "failed" || !failed.CanResume {
		t.Fatalf("interrupted download should be resumable, got %#v", failed)
	}
	if _, err := os.Stat(filepath.Join(root, "firmware.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial bytes were committed to final destination: %v", err)
	}

	if _, err := manager.ResumeTask(task.ID); err != nil {
		t.Fatalf("resume download: %v", err)
	}
	completed := waitTask(t, manager, task.ID)
	if completed.State != "completed" || completed.CanResume {
		t.Fatalf("resumed download state = %#v", completed)
	}
	data, err := os.ReadFile(filepath.Join(root, "firmware.bin"))
	if err != nil || string(data) != "receiver-firmware-image" {
		t.Fatalf("resumed download = %q, %v", data, err)
	}
	partials, _ := filepath.Glob(filepath.Join(root, ".firmware.bin.dccnext-*.part"))
	if len(partials) != 0 {
		t.Fatalf("partial download was not cleaned after commit: %v", partials)
	}
}

func TestManagerResumesInterruptedUploadAndCommitsAtomically(t *testing.T) {
	root := t.TempDir()
	local := &LocalFS{root: root}
	if err := os.WriteFile(filepath.Join(root, "settings.bin"), []byte("receiver-settings-backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := newResumableMemoryTransfer()
	adapter.files["/settings.bin"] = []byte("old-version")
	manager := NewManager()
	session := &managedSession{info: SessionInfo{ID: "test-session", ProfileID: "device"}, adapter: adapter}
	manager.sessions["test-session"] = session
	defer manager.CloseAll()

	task, err := manager.StartTransfer("test-session", "upload", "settings.bin", "/settings.bin", true, false, local)
	if err != nil {
		t.Fatal(err)
	}
	failed := waitTask(t, manager, task.ID)
	if failed.State != "failed" || !failed.CanResume {
		t.Fatalf("interrupted upload should be resumable, got %#v", failed)
	}
	adapter.mu.Lock()
	oldDuringUpload := string(adapter.files["/settings.bin"])
	adapter.mu.Unlock()
	if oldDuringUpload != "old-version" {
		t.Fatalf("existing remote file was changed before commit: %q", oldDuringUpload)
	}

	if _, err := manager.ResumeTask(task.ID); err != nil {
		t.Fatalf("resume upload: %v", err)
	}
	completed := waitTask(t, manager, task.ID)
	if completed.State != "completed" || completed.CanResume {
		t.Fatalf("resumed upload state = %#v", completed)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if got := string(adapter.files["/settings.bin"]); got != "receiver-settings-backup" {
		t.Fatalf("committed remote file = %q", got)
	}
	for name := range adapter.files {
		if strings.Contains(name, ".dccnext-") {
			t.Fatalf("temporary remote upload remains after commit: %s", name)
		}
	}
}

func TestResumeTaskRejectsChangedUploadSource(t *testing.T) {
	root := t.TempDir()
	local := &LocalFS{root: root}
	source := filepath.Join(root, "source.bin")
	if err := os.WriteFile(source, []byte("original-data"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := newResumableMemoryTransfer()
	manager := NewManager()
	manager.sessions["test-session"] = &managedSession{info: SessionInfo{ID: "test-session", ProfileID: "device"}, adapter: adapter}
	defer manager.CloseAll()
	task, err := manager.StartTransfer("test-session", "upload", "source.bin", "/source.bin", false, false, local)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitTask(t, manager, task.ID); result.State != "failed" {
		t.Fatalf("initial upload state = %#v", result)
	}
	if err := os.WriteFile(source, []byte("changed-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.ResumeTask(task.ID); err == nil || !strings.Contains(err.Error(), "source changed") {
		t.Fatalf("changed upload source was not rejected: %v", err)
	}
}

func TestResumeTaskRebindsAfterReconnect(t *testing.T) {
	root := t.TempDir()
	local := &LocalFS{root: root}
	adapter := newResumableMemoryTransfer()
	adapter.files["/remote.bin"] = []byte("receiver-image-after-reconnect")
	manager := NewManager()
	original := &managedSession{info: SessionInfo{ID: "old-session", ProfileID: "device", Protocol: "ftp", Host: "192.0.2.10", Port: 21}, adapter: adapter}
	manager.sessions[original.info.ID] = original
	defer manager.CloseAll()

	task, err := manager.StartTransfer(original.info.ID, "download", "image.bin", "/remote.bin", false, false, local)
	if err != nil {
		t.Fatal(err)
	}
	if failed := waitTask(t, manager, task.ID); failed.State != "failed" || !failed.CanResume {
		t.Fatalf("initial transfer did not fail resumably: %#v", failed)
	}

	delete(manager.sessions, original.info.ID)
	reconnected := &managedSession{info: SessionInfo{ID: "new-session", ProfileID: "device", Protocol: "ftp", Host: "192.0.2.10", Port: 21, OpenedAt: time.Now().UTC()}, adapter: adapter}
	manager.sessions[reconnected.info.ID] = reconnected
	queued, err := manager.ResumeTask(task.ID)
	if err != nil {
		t.Fatalf("resume after reconnect: %v", err)
	}
	if queued.SessionID != reconnected.info.ID {
		t.Fatalf("task was not rebound to the new session: %#v", queued)
	}
	completed := waitTask(t, manager, task.ID)
	if completed.State != "completed" {
		t.Fatalf("reconnected task state = %#v", completed)
	}
}

func TestRestartTaskClearsPartialAndStartsFromZero(t *testing.T) {
	root := t.TempDir()
	local := &LocalFS{root: root}
	adapter := newResumableMemoryTransfer()
	adapter.files["/remote.bin"] = []byte("complete-image-content")
	manager := NewManager()
	manager.sessions["test-session"] = &managedSession{info: SessionInfo{ID: "test-session", ProfileID: "device"}, adapter: adapter}
	defer manager.CloseAll()

	task, err := manager.StartTransfer("test-session", "download", "image.bin", "/remote.bin", false, false, local)
	if err != nil {
		t.Fatal(err)
	}
	if failed := waitTask(t, manager, task.ID); failed.State != "failed" || !failed.CanResume {
		t.Fatalf("initial transfer did not fail: %#v", failed)
	}
	queued, err := manager.RestartTask(task.ID)
	if err != nil {
		t.Fatalf("restart from zero: %v", err)
	}
	if queued.State != "queued" || queued.Progress.BytesDone != 0 {
		t.Fatalf("restart did not reset transfer progress: %#v", queued)
	}
	completed := waitTask(t, manager, task.ID)
	if completed.State != "completed" {
		t.Fatalf("restarted task state = %#v", completed)
	}
	data, err := os.ReadFile(filepath.Join(root, "image.bin"))
	if err != nil || string(data) != "complete-image-content" {
		t.Fatalf("restarted file = %q, %v", data, err)
	}
}

func TestClearFinishedTasksRemovesOwnedPartialFiles(t *testing.T) {
	root := t.TempDir()
	local := &LocalFS{root: root}
	adapter := newResumableMemoryTransfer()
	if err := os.WriteFile(filepath.Join(root, "upload.bin"), []byte("upload-payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter.files["/download.bin"] = []byte("download-payload")
	manager := NewManager()
	session := &managedSession{info: SessionInfo{ID: "test-session", ProfileID: "device"}, adapter: adapter}
	manager.sessions[session.info.ID] = session
	defer manager.CloseAll()

	download, err := manager.StartTransfer(session.info.ID, "download", "download.bin", "/download.bin", false, false, local)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitTask(t, manager, download.ID); result.State != "failed" {
		t.Fatalf("download state = %#v", result)
	}
	upload, err := manager.StartTransfer(session.info.ID, "upload", "upload.bin", "/upload.bin", false, false, local)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitTask(t, manager, upload.ID); result.State != "failed" {
		t.Fatalf("upload state = %#v", result)
	}

	if removed := manager.ClearFinishedTasks(); removed != 2 {
		t.Fatalf("removed %d tasks, want 2", removed)
	}
	if _, err := os.Stat(partialDownloadPath(filepath.Join(root, "download.bin"), download.ID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("download partial remains after clearing task: %v", err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	for remote := range adapter.files {
		if strings.Contains(remote, ".dccnext-") {
			t.Fatalf("remote partial remains after clearing task: %s", remote)
		}
	}
}

func TestCommitRemoteFileRecoversPriorBackup(t *testing.T) {
	adapter := newMemoryTransfer()
	adapter.files["/config.bin.dccnext-task.part"] = []byte("new-complete-content")
	adapter.files["/.config.bin.dccnext-backup-task.part"] = []byte("previous-final-content")
	if err := commitRemoteFile(context.Background(), adapter, "/config.bin.dccnext-task.part", "/config.bin", true, "task"); err != nil {
		t.Fatalf("recover interrupted remote commit: %v", err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if got := string(adapter.files["/config.bin"]); got != "new-complete-content" {
		t.Fatalf("recovered final file = %q", got)
	}
	if _, ok := adapter.files["/.config.bin.dccnext-backup-task.part"]; ok {
		t.Fatal("recovered backup was not removed")
	}
}

func TestManagerRejectsOverwriteAndCancelsFinishedTask(t *testing.T) {
	root := t.TempDir()
	local := &LocalFS{root: root}
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	adapter := newMemoryTransfer()
	adapter.files["/remote.txt"] = []byte("remote")
	manager := NewManager()
	manager.sessions["test-session"] = &managedSession{info: SessionInfo{ID: "test-session", ProfileID: "device"}, adapter: adapter}
	defer manager.CloseAll()
	if _, err := manager.StartTransfer("test-session", "download", "target.txt", "/remote.txt", false, false, local); err == nil {
		t.Fatal("expected overwrite consent requirement")
	}
	task, err := manager.StartTransfer("test-session", "download", "target.txt", "/remote.txt", true, false, local)
	if err != nil {
		t.Fatal(err)
	}
	if result := waitTask(t, manager, task.ID); result.State != "completed" {
		t.Fatalf("download failed: %#v", result)
	}
	if err := manager.CancelTask(task.ID); err == nil {
		t.Fatal("cancelled an already completed task")
	}
	data, err := os.ReadFile(filepath.Join(root, "target.txt"))
	if err != nil || string(data) != "remote" {
		t.Fatalf("download result = %q, %v", data, err)
	}
}

var _ core.FileTransfer = (*memoryTransfer)(nil)

func TestRemoteOverwriteRequiresExplicitConfirmation(t *testing.T) {
	adapter := newMemoryTransfer()
	adapter.files["/existing.bin"] = []byte("original")
	adapter.dirs["/existing-folder"] = true
	if err := checkRemoteDestination(context.Background(), adapter, "/existing.bin", false, false); err == nil {
		t.Fatal("allowed overwrite without confirmation")
	}
	if err := checkRemoteDestination(context.Background(), adapter, "/existing.bin", false, true); err != nil {
		t.Fatalf("confirmed file overwrite rejected: %v", err)
	}
	if err := checkRemoteDestination(context.Background(), adapter, "/existing-folder", true, true); err != nil {
		t.Fatalf("confirmed folder merge rejected: %v", err)
	}
	if err := checkRemoteDestination(context.Background(), adapter, "/existing-folder", false, true); err == nil {
		t.Fatal("allowed file overwrite to a directory")
	}
}

func TestClearFinishedTasksKeepsActiveTransfers(t *testing.T) {
	manager := NewManager()
	manager.mu.Lock()
	manager.tasks["done"] = &taskEntry{info: TaskInfo{ID: "done", State: "completed"}}
	manager.tasks["failed"] = &taskEntry{info: TaskInfo{ID: "failed", State: "failed"}}
	manager.tasks["running"] = &taskEntry{info: TaskInfo{ID: "running", State: "running"}}
	manager.mu.Unlock()

	if removed := manager.ClearFinishedTasks(); removed != 2 {
		t.Fatalf("removed %d finished tasks, want 2", removed)
	}
	remaining := manager.Tasks()
	if len(remaining) != 1 || remaining[0].ID != "running" {
		t.Fatalf("remaining tasks = %#v, want only running", remaining)
	}
}

func TestParseRemoteModeSupportsOctalAndSymbolicModes(t *testing.T) {
	cases := []struct {
		input string
		want  uint32
	}{
		{"755", 0o755},
		{"0644", 0o644},
		{"1777", 0o1777},
		{"-rwxr-xr-x", 0o755},
		{"-rw-r--r--", 0o644},
		{"-rwsr-sr-t", 0o7755},
		{"drwxr-xr-T", 0o1754},
	}
	for _, tc := range cases {
		got, ok := parseRemoteMode(tc.input)
		if !ok || got != tc.want {
			t.Errorf("parseRemoteMode(%q) = %#o, %v; want %#o", tc.input, got, ok, tc.want)
		}
	}
	for _, invalid := range []string{"", "0899", "-rwxr-xq-x", "qwxr-xr-x", "12345"} {
		if _, ok := parseRemoteMode(invalid); ok {
			t.Errorf("parseRemoteMode(%q) unexpectedly succeeded", invalid)
		}
	}
}

func TestCommitRemoteFilePreservesExistingMode(t *testing.T) {
	adapter := newMemoryTransfer()
	adapter.files["/settings.conf"] = []byte("old")
	adapter.files["/.settings.conf.dccnext-task.part"] = []byte("new")
	adapter.modes["/settings.conf"] = 0o755
	adapter.modes["/.settings.conf.dccnext-task.part"] = 0o600
	if err := commitRemoteFile(context.Background(), adapter, "/.settings.conf.dccnext-task.part", "/settings.conf", true, "task"); err != nil {
		t.Fatalf("commit failed: %v", err)
	}
	adapter.mu.Lock()
	defer adapter.mu.Unlock()
	if got := string(adapter.files["/settings.conf"]); got != "new" {
		t.Fatalf("committed file contents = %q", got)
	}
	if got := adapter.modes["/settings.conf"]; got != 0o755 {
		t.Fatalf("committed file mode = %04o, want 0755", got)
	}
}

func TestTasksClearStaleTransferSpeedAndETA(t *testing.T) {
	manager := NewManager()
	staleAt := time.Now().Add(-3 * time.Second)
	manager.mu.Lock()
	manager.tasks["running"] = &taskEntry{
		info: TaskInfo{
			ID:    "running",
			State: "running",
			Progress: core.TransferProgress{
				BytesDone:           4096,
				BytesTotal:          8192,
				SpeedBytesPerSecond: 2048,
				ETASeconds:          2,
			},
		},
		lastProgressAt: staleAt,
		lastProgress:   4096,
		smoothedSpeed:  2048,
	}
	manager.mu.Unlock()

	tasks := manager.Tasks()
	if len(tasks) != 1 {
		t.Fatalf("Tasks() returned %d entries, want 1", len(tasks))
	}
	if tasks[0].Progress.SpeedBytesPerSecond != 0 || tasks[0].Progress.ETASeconds != 0 {
		t.Fatalf("stale speed/ETA remained visible: %#v", tasks[0].Progress)
	}
	manager.mu.Lock()
	entry := manager.tasks["running"]
	if entry.smoothedSpeed != 0 || entry.lastProgress != 4096 || time.Since(entry.lastProgressAt) > time.Second {
		manager.mu.Unlock()
		t.Fatalf("stale speed baseline was not reset: speed=%v bytes=%d at=%v", entry.smoothedSpeed, entry.lastProgress, entry.lastProgressAt)
	}
	manager.mu.Unlock()
}
