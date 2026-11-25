package filetransfer

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

type TaskInfo struct {
	ID          string                `json:"id"`
	SessionID   string                `json:"session_id"`
	Direction   string                `json:"direction"`
	IsDirectory bool                  `json:"is_directory"`
	LocalPath   string                `json:"local_path"`
	RemotePath  string                `json:"remote_path"`
	State       string                `json:"state"`
	Progress    core.TransferProgress `json:"progress"`
	Error       string                `json:"error,omitempty"`
	Warning     string                `json:"warning,omitempty"`

	Staged    bool      `json:"staged,omitempty"`
	CanResume bool      `json:"can_resume"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type taskEntry struct {
	info            TaskInfo
	cancel          context.CancelFunc
	session         *managedSession
	local           *LocalFS
	localAbsolute   string
	partialPath     string
	remotePartial   string
	overwrite       bool
	sourceSize      int64
	sourceModTimeNS int64
	lastProgressAt  time.Time
	lastProgress    int64
	smoothedSpeed   float64
}

func (m *Manager) StartTransfer(sessionID, direction, localRelative, remotePath string, overwrite, isDirectory bool, local *LocalFS) (TaskInfo, error) {
	return m.startTransfer(sessionID, direction, localRelative, remotePath, overwrite, isDirectory, local, false)
}

func (m *Manager) StartStagedTransfer(sessionID, direction, stagedRelative, remotePath string, overwrite bool, staging *LocalFS) (TaskInfo, error) {
	return m.startTransfer(sessionID, direction, stagedRelative, remotePath, overwrite, false, staging, true)
}

func (m *Manager) startTransfer(sessionID, direction, localRelative, remotePath string, overwrite, isDirectory bool, local *LocalFS, staged bool) (TaskInfo, error) {
	if local == nil {
		return TaskInfo{}, errors.New("local file system is unavailable")
	}
	session, err := m.get(sessionID)
	if err != nil {
		return TaskInfo{}, err
	}
	if direction != "upload" && direction != "download" {
		return TaskInfo{}, errors.New("transfer direction must be upload or download")
	}
	if err := validateRemotePath(remotePath); err != nil {
		return TaskInfo{}, err
	}
	var localAbsolute string
	var sourceInfo os.FileInfo
	if direction == "upload" {
		localAbsolute, _, err = local.Resolve(localRelative, false)
		if err != nil {
			return TaskInfo{}, err
		}
		info, statErr := os.Stat(localAbsolute)
		if statErr != nil {
			return TaskInfo{}, statErr
		}
		if info.IsDir() != isDirectory {
			return TaskInfo{}, errors.New("selected local file type changed; refresh the local panel")
		}
		if !info.IsDir() {
			file, openedInfo, openErr := local.Open(localRelative)
			if openErr != nil {
				return TaskInfo{}, openErr
			}
			_ = file.Close()
			sourceInfo = openedInfo
		}
	} else {
		localAbsolute, _, err = local.Resolve(localRelative, true)
		if err != nil {
			return TaskInfo{}, err
		}
		if info, statErr := os.Lstat(localAbsolute); statErr == nil {
			if isDirectory && !info.IsDir() {
				return TaskInfo{}, errors.New("download folder destination exists and is not a directory")
			}
			if !isDirectory && info.IsDir() {
				return TaskInfo{}, errors.New("download file destination is a directory")
			}
			if !overwrite {
				return TaskInfo{}, errors.New("local destination already exists; enable overwrite to merge or replace it")
			}
		} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return TaskInfo{}, statErr
		}
	}
	id, err := newFileID()
	if err != nil {
		return TaskInfo{}, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().UTC()
	_, supportsResume := session.adapter.(core.ResumableFileTransfer)
	canResume := supportsResume && !isDirectory
	entry := &taskEntry{
		info:   TaskInfo{ID: id, SessionID: sessionID, Direction: direction, LocalPath: localRelative, RemotePath: remotePath, IsDirectory: isDirectory, State: "queued", CanResume: canResume, Staged: staged, CreatedAt: now, UpdatedAt: now},
		cancel: cancel, session: session, local: local, localAbsolute: localAbsolute, overwrite: overwrite,
	}
	if direction == "download" && !isDirectory {
		entry.partialPath = partialDownloadPath(localAbsolute, id)
	}
	if direction == "upload" && !isDirectory {
		entry.remotePartial = partialRemotePath(remotePath, id)
		if sourceInfo != nil {
			entry.sourceSize = sourceInfo.Size()
			entry.sourceModTimeNS = sourceInfo.ModTime().UnixNano()
		}
	}
	var pruned []*taskEntry
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		cancel()
		return TaskInfo{}, errors.New("file transfer manager is closed")
	}
	if len(m.tasks) >= maxTransferTasks {
		pruned = m.pruneTasksLocked()
		if len(m.tasks) >= maxTransferTasks {
			m.mu.Unlock()
			for _, stale := range pruned {
				m.cleanupTaskArtifacts(stale)
			}
			cancel()
			return TaskInfo{}, errors.New("transfer task queue is full")
		}
	}
	m.tasks[id] = entry
	m.mu.Unlock()
	for _, stale := range pruned {
		m.cleanupTaskArtifacts(stale)
	}
	initial := entry.info
	go m.runTransfer(ctx, entry, false)
	return initial, nil
}

func partialDownloadPath(target, id string) string {
	name := "." + filepath.Base(target) + ".dcclite-" + id + ".part"
	return filepath.Join(filepath.Dir(target), name)
}

func partialRemotePath(target, id string) string {
	return path.Join(path.Dir(target), "."+path.Base(target)+".dcclite-"+id+".part")
}

func (m *Manager) runTransfer(ctx context.Context, task *taskEntry, resume bool) {
	defer task.cancel()
	select {
	case m.queue <- struct{}{}:
	case <-ctx.Done():
		m.finishTask(task, "cancelled", "")
		return
	}
	defer func() { <-m.queue }()
	m.updateTask(task, func(info *TaskInfo) { info.State = "running" })
	started := time.Now()
	flog.Info("transfer started", "task", task.info.ID, "direction", task.info.Direction, "remote_path", task.info.RemotePath, "resume", resume)
	session := task.session
	if session == nil {
		m.finishTask(task, "failed", "file transfer session is unavailable")
		return
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if err := ctx.Err(); err != nil {
		m.finishTask(task, "cancelled", "")
		return
	}
	if session.closed {
		m.finishTask(task, "failed", "file transfer session closed")
		return
	}
	progress := func(p core.TransferProgress) { m.updateTransferProgress(task, p) }
	var err error
	resumable, supportsResume := session.adapter.(core.ResumableFileTransfer)
	if task.info.Direction == "upload" {
		err = checkRemoteDestination(ctx, session.adapter, task.info.RemotePath, task.info.IsDirectory, task.overwrite)
		if err == nil && task.info.IsDirectory {
			err = uploadDirectory(ctx, session.adapter, task.localAbsolute, task.info.RemotePath, task.overwrite, progress)
		} else if err == nil && resume {
			if !supportsResume {
				err = errors.New("this transfer protocol cannot resume partial uploads")
			} else {
				err = resumable.ResumeUpload(ctx, task.localAbsolute, task.remotePartial, progress)
			}
		} else if err == nil {
			destination := task.info.RemotePath
			if supportsResume {
				destination = task.remotePartial
			}
			err = session.adapter.Upload(ctx, task.localAbsolute, destination, progress)
		}
		if err == nil && !task.info.IsDirectory && supportsResume {
			m.preserveUploadTime(task, session.adapter, ctx)
			err = commitRemoteFile(ctx, session.adapter, task.remotePartial, task.info.RemotePath, task.overwrite, task.info.ID)
		}
	} else if task.info.IsDirectory {
		err = downloadDirectory(ctx, session.adapter, task.local, task.info.RemotePath, task.info.LocalPath, task.overwrite, progress)
	} else if supportsResume {
		if resume {
			err = resumable.ResumeDownload(ctx, task.info.RemotePath, task.partialPath, progress)
		} else {
			err = resumable.DownloadPartial(ctx, task.info.RemotePath, task.partialPath, progress)
		}
		if err == nil {
			err = task.local.ReplaceTemp(task.partialPath, task.info.LocalPath, task.overwrite)
		}
	} else {
		err = downloadFile(ctx, session.adapter, task.local, task.info.RemotePath, task.info.LocalPath, task.overwrite, progress)
	}
	if err != nil {
		state := "failed"
		if errors.Is(err, context.Canceled) {
			state = "cancelled"
		}
		m.finishTask(task, state, err.Error())
		if state == "cancelled" {
			flog.Info("transfer cancelled", "task", task.info.ID, "remote_path", task.info.RemotePath)
		} else {
			flog.Error("transfer failed", "task", task.info.ID, "direction", task.info.Direction, "remote_path", task.info.RemotePath, "error", err, "after", time.Since(started).Round(time.Millisecond))
		}
		return
	}
	if task.partialPath != "" {
		_ = os.Remove(task.partialPath)
	}
	m.updateTask(task, func(info *TaskInfo) {
		info.State = "completed"
		info.CanResume = false
		info.Progress.Fraction = 1
		info.UpdatedAt = time.Now().UTC()
	})

	m.mu.Lock()
	done := task.info.Progress.BytesDone
	m.mu.Unlock()
	flog.Info("transfer completed", "task", task.info.ID, "direction", task.info.Direction, "remote_path", task.info.RemotePath, "bytes", done, "took", time.Since(started).Round(time.Millisecond))

	if task.info.Staged && task.info.Direction == "upload" {
		removeStagedFiles(task)
	}
}

func commitRemoteFile(ctx context.Context, adapter core.FileTransfer, partialPath, remotePath string, overwrite bool, taskID string) error {
	entries, err := adapter.List(ctx, path.Dir(remotePath))
	if err != nil {
		return fmt.Errorf("check remote destination before commit: %w", err)
	}
	exists := false
	backup := partialRemotePath(remotePath, "backup-"+taskID)
	backupExists := false
	var preservedMode uint32
	preserveMode := false
	for _, entry := range entries {
		if entry.Name == path.Base(remotePath) {
			if entry.IsDir {
				return errors.New("remote destination became a directory during transfer")
			}
			exists = true
			if !strings.HasPrefix(entry.Mode, "l") {
				preservedMode, preserveMode = parseRemoteMode(entry.Mode)
			}
		}
		if entry.Name == path.Base(backup) {
			backupExists = true
		}
	}
	if !exists {
		if err := adapter.Rename(ctx, partialPath, remotePath); err != nil {
			return err
		}

		if backupExists {
			_ = adapter.Remove(ctx, backup, false)
		}
		return nil
	}
	if !overwrite {
		return errors.New("remote destination appeared during transfer; overwrite was not confirmed")
	}
	if preserveMode {
		if err := adapter.Chmod(ctx, partialPath, preservedMode); err != nil {
			return fmt.Errorf("could not preserve existing permissions %04o before replacing the remote file: %w", preservedMode, err)
		}
	}
	if err := adapter.Rename(ctx, partialPath, remotePath); err == nil {
		return nil
	}
	if backupExists {
		return fmt.Errorf("a previous commit backup already exists at %s; refusing to overwrite it automatically", backup)
	}
	if err := adapter.Rename(ctx, remotePath, backup); err != nil {
		return fmt.Errorf("preserve existing remote destination: %w", err)
	}
	if err := adapter.Rename(ctx, partialPath, remotePath); err != nil {
		if rollbackErr := adapter.Rename(ctx, backup, remotePath); rollbackErr != nil {
			return fmt.Errorf("commit remote file failed (%v) and restore backup failed (%v); previous file is preserved at %s", err, rollbackErr, backup)
		}
		return err
	}
	_ = adapter.Remove(ctx, backup, false)
	return nil
}

func parseRemoteMode(value string) (uint32, bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, false
	}
	if len(value) <= 4 {
		for _, digit := range value {
			if digit < '0' || digit > '7' {
				return 0, false
			}
		}
		mode, err := strconv.ParseUint(value, 8, 32)
		return uint32(mode), err == nil && mode <= 0o7777
	}
	if len(value) == 10 {
		value = value[1:]
	}
	if len(value) != 9 {
		return 0, false
	}
	var mode uint32
	for index, shift := range []uint32{6, 3, 0} {
		base := index * 3
		if value[base] == 'r' {
			mode |= 0o4 << shift
		} else if value[base] != '-' {
			return 0, false
		}
		if value[base+1] == 'w' {
			mode |= 0o2 << shift
		} else if value[base+1] != '-' {
			return 0, false
		}
		execute := value[base+2]
		switch execute {
		case 'x':
			mode |= 0o1 << shift
		case '-':
		case 's', 'S':
			if index == 0 {
				mode |= 0o4000
			} else if index == 1 {
				mode |= 0o2000
			} else {
				return 0, false
			}
			if execute == 's' {
				mode |= 0o1 << shift
			}
		case 't', 'T':
			if index != 2 {
				return 0, false
			}
			mode |= 0o1000
			if execute == 't' {
				mode |= 0o1
			}
		default:
			return 0, false
		}
	}
	return mode, true
}

func (m *Manager) preserveUploadTime(task *taskEntry, adapter core.FileTransfer, ctx context.Context) {
	preserver, ok := adapter.(core.FileTimePreserver)
	if !ok || task.sourceModTimeNS == 0 {
		return
	}
	modified := time.Unix(0, task.sourceModTimeNS)
	if err := preserver.SetModTime(ctx, task.remotePartial, modified); err != nil && !errors.Is(err, core.ErrMetadataUnsupported) {
		m.updateTask(task, func(info *TaskInfo) {
			info.Warning = "File contents were uploaded, but the modification time could not be preserved: " + err.Error()
		})
	}
}

func (m *Manager) updateTask(task *taskEntry, update func(*TaskInfo)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.tasks[task.info.ID]
	if !ok {
		return
	}
	update(&current.info)
	current.info.UpdatedAt = time.Now().UTC()
}

func (m *Manager) updateTransferProgress(task *taskEntry, progress core.TransferProgress) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.tasks[task.info.ID]
	if !ok {
		return
	}
	now := time.Now()
	progress, task.smoothedSpeed, task.lastProgress, task.lastProgressAt = estimateTransferProgress(
		progress, task.lastProgress, task.lastProgressAt, task.smoothedSpeed, now,
	)
	current.info.Progress = progress
	current.info.UpdatedAt = now.UTC()
}

func estimateTransferProgress(progress core.TransferProgress, previousBytes int64, previousAt time.Time, previousSpeed float64, now time.Time) (core.TransferProgress, float64, int64, time.Time) {
	if previousAt.IsZero() || progress.BytesDone < previousBytes {
		progress.SpeedBytesPerSecond = 0
		progress.ETASeconds = 0
		return progress, 0, progress.BytesDone, now
	}
	elapsed := now.Sub(previousAt).Seconds()
	delta := progress.BytesDone - previousBytes
	if elapsed >= 0.25 && delta > 0 {
		instant := float64(delta) / elapsed
		if previousSpeed <= 0 {
			previousSpeed = instant
		} else {

			previousSpeed = previousSpeed*0.70 + instant*0.30
		}
		previousBytes = progress.BytesDone
		previousAt = now
	} else if elapsed >= 2 {

		previousSpeed = 0
		previousBytes = progress.BytesDone
		previousAt = now
	}
	if previousSpeed > 0 {
		progress.SpeedBytesPerSecond = int64(previousSpeed + 0.5)
		remaining := progress.BytesTotal - progress.BytesDone
		if progress.BytesTotal > 0 && remaining > 0 {
			progress.ETASeconds = int64(math.Ceil(float64(remaining) / previousSpeed))
		} else {
			progress.ETASeconds = 0
		}
	} else {
		progress.SpeedBytesPerSecond = 0
		progress.ETASeconds = 0
	}
	return progress, previousSpeed, previousBytes, previousAt
}

func (m *Manager) finishTask(task *taskEntry, state, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, ok := m.tasks[task.info.ID]
	if !ok {
		return
	}
	current.info.State = state
	current.info.Error = message
	current.info.UpdatedAt = time.Now().UTC()
	_, supportsResume := current.session.adapter.(core.ResumableFileTransfer)
	current.info.CanResume = (state == "failed" || state == "cancelled") && !current.info.IsDirectory && supportsResume
}

func (m *Manager) ResumeTask(id string) (TaskInfo, error) {
	m.mu.Lock()
	entry, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return TaskInfo{}, ErrTaskNotFound
	}
	if entry.info.State != "failed" && entry.info.State != "cancelled" {
		m.mu.Unlock()
		return TaskInfo{}, errors.New("only failed or cancelled transfers can be resumed")
	}
	if !entry.info.CanResume || entry.info.IsDirectory {
		m.mu.Unlock()
		return TaskInfo{}, errors.New("this transfer cannot be resumed; start it again from the file panel")
	}
	if m.closed || entry.session == nil {
		m.mu.Unlock()
		return TaskInfo{}, errors.New("file transfer manager is closed")
	}
	resumeSession := m.findTaskSessionLocked(entry)
	if resumeSession == nil {
		m.mu.Unlock()
		return TaskInfo{}, errors.New("reconnect to the same saved site and protocol before resuming this transfer")
	}
	entry.session = resumeSession
	entry.info.SessionID = resumeSession.info.ID
	if entry.info.Direction == "upload" {
		info, err := os.Stat(entry.localAbsolute)
		if err != nil || !info.Mode().IsRegular() {
			m.mu.Unlock()
			return TaskInfo{}, errors.New("the local upload source is no longer available")
		}
		if info.Size() != entry.sourceSize || info.ModTime().UnixNano() != entry.sourceModTimeNS {
			m.mu.Unlock()
			return TaskInfo{}, errors.New("the local upload source changed; start a new transfer to avoid corrupting the remote file")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	entry.cancel = cancel
	entry.info.State = "queued"
	entry.info.Error = ""
	entry.info.Warning = ""
	entry.info.CanResume = false
	entry.lastProgressAt = time.Time{}
	entry.lastProgress = 0
	entry.smoothedSpeed = 0
	entry.info.UpdatedAt = time.Now().UTC()
	initial := entry.info
	m.mu.Unlock()
	go m.runTransfer(ctx, entry, true)
	return initial, nil
}

func (m *Manager) RestartTask(id string) (TaskInfo, error) {
	m.mu.Lock()
	entry, ok := m.tasks[id]
	if !ok {
		m.mu.Unlock()
		return TaskInfo{}, ErrTaskNotFound
	}
	if entry.info.State != "failed" && entry.info.State != "cancelled" {
		m.mu.Unlock()
		return TaskInfo{}, errors.New("only failed or cancelled transfers can be restarted")
	}
	if m.closed || entry.session == nil {
		m.mu.Unlock()
		return TaskInfo{}, errors.New("file transfer manager is closed")
	}
	resumeSession := m.findTaskSessionLocked(entry)
	if resumeSession == nil {
		m.mu.Unlock()
		return TaskInfo{}, errors.New("reconnect to the same saved site and protocol before restarting this transfer")
	}
	if entry.info.Direction == "upload" && !entry.info.IsDirectory {
		info, err := os.Stat(entry.localAbsolute)
		if err != nil || !info.Mode().IsRegular() {
			m.mu.Unlock()
			return TaskInfo{}, errors.New("the local upload source is no longer available")
		}
		entry.sourceSize = info.Size()
		entry.sourceModTimeNS = info.ModTime().UnixNano()
	}
	if entry.partialPath != "" {
		if err := os.Remove(entry.partialPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			m.mu.Unlock()
			return TaskInfo{}, fmt.Errorf("remove previous partial download: %w", err)
		}
	}
	entry.session = resumeSession
	entry.info.SessionID = resumeSession.info.ID
	ctx, cancel := context.WithCancel(context.Background())
	entry.cancel = cancel
	entry.info.State = "queued"
	entry.info.Error = ""
	entry.info.Warning = ""
	entry.info.CanResume = false
	entry.info.Progress = core.TransferProgress{}
	entry.lastProgressAt = time.Time{}
	entry.lastProgress = 0
	entry.smoothedSpeed = 0
	entry.info.UpdatedAt = time.Now().UTC()
	initial := entry.info
	m.mu.Unlock()
	go m.runTransfer(ctx, entry, false)
	return initial, nil
}

func (m *Manager) findTaskSessionLocked(entry *taskEntry) *managedSession {
	if entry == nil || entry.session == nil {
		return nil
	}
	if current := m.sessions[entry.info.SessionID]; current != nil {
		return current
	}
	original := entry.session.info
	var match *managedSession
	for _, candidate := range m.sessions {
		if candidate.info.ProfileID != original.ProfileID || candidate.info.Protocol != original.Protocol || candidate.info.Host != original.Host || candidate.info.Port != original.Port {
			continue
		}
		if match == nil || candidate.info.OpenedAt.After(match.info.OpenedAt) {
			match = candidate
		}
	}
	return match
}

func (m *Manager) Task(id string) (TaskInfo, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	entry, ok := m.tasks[id]
	if !ok {
		return TaskInfo{}, ErrTaskNotFound
	}
	m.clearStaleTransferRateLocked(entry, time.Now())
	return entry.info, nil
}

func (m *Manager) Tasks() []TaskInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]TaskInfo, 0, len(m.tasks))
	now := time.Now()
	for _, entry := range m.tasks {
		m.clearStaleTransferRateLocked(entry, now)
		out = append(out, entry.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func (m *Manager) clearStaleTransferRateLocked(entry *taskEntry, now time.Time) {
	if entry == nil || entry.info.State != "running" || entry.lastProgressAt.IsZero() || now.Sub(entry.lastProgressAt) < 2*time.Second {
		return
	}
	entry.smoothedSpeed = 0
	entry.lastProgress = entry.info.Progress.BytesDone
	entry.lastProgressAt = now
	entry.info.Progress.SpeedBytesPerSecond = 0
	entry.info.Progress.ETASeconds = 0
}

func (m *Manager) ClearFinishedTasks() int {
	m.mu.Lock()
	entries := make([]*taskEntry, 0)
	removed := 0
	for id, entry := range m.tasks {
		switch entry.info.State {
		case "completed", "failed", "cancelled":
			if entry.info.State != "completed" && entry.remotePartial != "" {
				if session := m.findTaskSessionLocked(entry); session != nil {
					entry.session = session
				}
			}
			delete(m.tasks, id)
			entries = append(entries, entry)
			removed++
		}
	}
	m.mu.Unlock()
	for _, entry := range entries {
		m.cleanupTaskArtifacts(entry)
	}
	return removed
}

func (m *Manager) cleanupTaskArtifacts(entry *taskEntry) {
	if entry == nil {
		return
	}
	removeStagedFiles(entry)
	if entry.partialPath != "" {
		_ = os.Remove(entry.partialPath)
	}
	if entry.info.State != "completed" && entry.remotePartial != "" && entry.session != nil {
		m.removeRemotePartial(entry.session, entry.remotePartial)
	}
}

func (m *Manager) removeRemotePartial(session *managedSession, remotePath string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed || ctx.Err() != nil {
		return
	}
	_ = session.adapter.Remove(ctx, remotePath, false)
}

func (m *Manager) CancelTask(id string) error {
	m.mu.Lock()
	entry, ok := m.tasks[id]
	if ok {
		switch entry.info.State {
		case "completed", "failed", "cancelled":
			m.mu.Unlock()
			return errors.New("transfer task is already finished")
		default:
			entry.cancel()
		}
	}
	m.mu.Unlock()
	if !ok {
		return ErrTaskNotFound
	}
	return nil
}

func (m *Manager) pruneTasksLocked() []*taskEntry {
	cutoff := time.Now().Add(-30 * time.Minute)
	var removed []*taskEntry
	for id, entry := range m.tasks {
		if entry.info.UpdatedAt.Before(cutoff) && (entry.info.State == "completed" || entry.info.State == "failed" || entry.info.State == "cancelled") {
			if entry.info.State != "completed" && entry.remotePartial != "" {
				if session := m.findTaskSessionLocked(entry); session != nil {
					entry.session = session
				}
			}
			delete(m.tasks, id)
			removed = append(removed, entry)
		}
	}

	if len(m.tasks) >= maxTransferTasks {
		var done []*taskEntry
		for _, entry := range m.tasks {
			if entry.info.State == "completed" {
				done = append(done, entry)
			}
		}
		sort.Slice(done, func(i, j int) bool { return done[i].info.UpdatedAt.Before(done[j].info.UpdatedAt) })
		for _, entry := range done {
			if len(m.tasks) < maxTransferTasks-maxTransferTasks/5 {
				break
			}
			delete(m.tasks, entry.info.ID)
			removed = append(removed, entry)
		}
	}
	return removed
}
