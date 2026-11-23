package filetransfer

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/core"
)

type fakeAdapter struct{ closed atomic.Int32 }

func (f *fakeAdapter) List(context.Context, string) ([]core.FileEntry, error) { return nil, nil }
func (f *fakeAdapter) Upload(context.Context, string, string, func(core.TransferProgress)) error {
	return nil
}
func (f *fakeAdapter) Download(context.Context, string, string, func(core.TransferProgress)) error {
	return nil
}
func (f *fakeAdapter) Mkdir(context.Context, string) error          { return nil }
func (f *fakeAdapter) Rename(context.Context, string, string) error { return nil }
func (f *fakeAdapter) Remove(context.Context, string, bool) error   { return nil }
func (f *fakeAdapter) Chmod(context.Context, string, uint32) error  { return nil }
func (f *fakeAdapter) Close() error                                 { f.closed.Add(1); return nil }

func newTestManager(t *testing.T) *Manager {
	t.Helper()
	m := NewManager()
	t.Cleanup(m.CloseAll)
	return m
}

func addSession(m *Manager, id, profile string, lastUsed time.Time) *fakeAdapter {
	a := &fakeAdapter{}
	m.mu.Lock()
	m.sessions[id] = &managedSession{
		info:     SessionInfo{ID: id, ProfileID: profile, Protocol: "ftp", OpenedAt: lastUsed},
		adapter:  a,
		lastUsed: lastUsed,
	}
	m.mu.Unlock()
	return a
}

func addTask(m *Manager, id, session, state string, updated time.Time) {
	m.mu.Lock()
	m.tasks[id] = &taskEntry{info: TaskInfo{ID: id, SessionID: session, State: state, UpdatedAt: updated}, cancel: func() {}}
	m.mu.Unlock()
}

func testProfile(id string) config.Profile {
	return config.Profile{ID: id, Name: "box", Host: "192.0.2.1", Username: "root", Enabled: true}
}

func TestIdleSessionPicksOldestIdleAndSkipsBusy(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	addSession(m, "old-busy", "profileAAAA", now.Add(-3*time.Hour))
	addSession(m, "mid-idle", "profileAAAA", now.Add(-2*time.Hour))
	addSession(m, "new-idle", "profileBBBB", now.Add(-1*time.Hour))
	addTask(m, "t1", "old-busy", "running", now)

	m.mu.Lock()
	defer m.mu.Unlock()
	if v := m.idleSessionLocked("profileAAAA", false); v == nil || v.info.ID != "mid-idle" {
		t.Fatalf("victim = %+v, want mid-idle", v)
	}
	if v := m.idleSessionLocked("profileBBBB", true); v == nil || v.info.ID != "new-idle" {
		t.Fatalf("device-only victim = %+v, want new-idle", v)
	}
}

func TestOpenReturnsLimitErrorWhenEverySessionIsBusy(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	for i := 0; i < maxFileSessions; i++ {
		id := fmt.Sprintf("s%d", i)
		addSession(m, id, fmt.Sprintf("profile%04d", i), now)
		addTask(m, "t"+id, id, "running", now)
	}
	_, err := m.Open(context.Background(), testProfile("profile9999"), "ftp", 21, "pw")
	if !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("err = %v, want ErrSessionLimit", err)
	}
}

func TestOpenPerDeviceLimitOnlyEvictsSameDevice(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	for i := 0; i < maxFileSessionsPerDevice; i++ {
		id := fmt.Sprintf("a%d", i)
		addSession(m, id, "profileAAAA", now)
		addTask(m, "t"+id, id, "queued", now)
	}
	other := addSession(m, "b0", "profileBBBB", now.Add(-time.Hour)) // idle, different device
	_, err := m.Open(context.Background(), testProfile("profileAAAA"), "ftp", 21, "pw")
	if !errors.Is(err, ErrSessionLimit) {
		t.Fatalf("err = %v, want ErrSessionLimit", err)
	}
	if other.closed.Load() != 0 {
		t.Fatal("an idle session of another device was evicted for a per-device limit")
	}
}

func TestOpenRejectsInvalidRequests(t *testing.T) {
	m := newTestManager(t)
	p := testProfile("profileAAAA")
	if _, err := m.Open(context.Background(), p, "sftp", 21, "x"); err == nil {
		t.Error("non-FTP protocol accepted")
	}
	if _, err := m.Open(context.Background(), p, "ftp", 0, "x"); err == nil {
		t.Error("port 0 accepted")
	}
	p.Enabled = false
	if _, err := m.Open(context.Background(), p, "ftp", 21, "x"); err == nil {
		t.Error("disabled device accepted")
	}
}

func TestCloseAllClosesEverySession(t *testing.T) {
	m := NewManager()
	a := addSession(m, "s1", "profileAAAA", time.Now())
	b := addSession(m, "s2", "profileBBBB", time.Now())
	m.CloseAll()
	if a.closed.Load() != 1 || b.closed.Load() != 1 {
		t.Fatalf("closed counts = %d, %d; want 1, 1", a.closed.Load(), b.closed.Load())
	}
	if len(m.Sessions()) != 0 {
		t.Fatal("sessions remain after CloseAll")
	}
	if _, err := m.Open(context.Background(), testProfile("profileAAAA"), "ftp", 21, "x"); err == nil {
		t.Fatal("Open succeeded on a closed manager")
	}
}

func TestCloseSessionCancelsItsActiveTasks(t *testing.T) {
	m := newTestManager(t)
	addSession(m, "s1", "profileAAAA", time.Now())
	cancelled := false
	m.mu.Lock()
	m.tasks["t1"] = &taskEntry{info: TaskInfo{ID: "t1", SessionID: "s1", State: "running"}, cancel: func() { cancelled = true }}
	m.mu.Unlock()
	if err := m.Close("s1"); err != nil {
		t.Fatal(err)
	}
	if !cancelled {
		t.Fatal("running task was not cancelled")
	}
	if err := m.Close("s1"); !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("second Close err = %v", err)
	}
}

func TestClearFinishedTasksKeepsActiveOnes(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	for id, state := range map[string]string{"a": "completed", "b": "failed", "c": "cancelled", "d": "running", "e": "queued"} {
		addTask(m, id, "s", state, now)
	}
	if n := m.ClearFinishedTasks(); n != 3 {
		t.Fatalf("removed %d, want 3", n)
	}
	if got := len(m.Tasks()); got != 2 {
		t.Fatalf("%d tasks left, want 2", got)
	}
}

func TestPruneTasksDropsOnlyOldFinishedTasks(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	addTask(m, "old-done", "s", "completed", now.Add(-time.Hour))
	addTask(m, "new-done", "s", "completed", now)
	addTask(m, "old-running", "s", "running", now.Add(-time.Hour))
	m.mu.Lock()
	removed := m.pruneTasksLocked()
	m.mu.Unlock()
	if len(removed) != 1 || removed[0].info.ID != "old-done" {
		t.Fatalf("removed = %v", removed)
	}
	if _, err := m.Task("old-running"); err != nil {
		t.Fatal("running task was pruned")
	}
}

func TestPruneTasksTrimsCompletedWhenOverCap(t *testing.T) {
	m := newTestManager(t)
	now := time.Now()
	for i := 0; i < maxTransferTasks; i++ {
		addTask(m, fmt.Sprintf("t%03d", i), "s", "completed", now.Add(time.Duration(i)*time.Second))
	}
	m.mu.Lock()
	m.pruneTasksLocked()
	left := len(m.tasks)
	_, oldestKept := m.tasks["t000"]
	m.mu.Unlock()
	if left >= maxTransferTasks {
		t.Fatalf("%d tasks left, want fewer than %d", left, maxTransferTasks)
	}
	if oldestKept {
		t.Fatal("oldest completed task should be dropped first")
	}
}

func TestParseRemoteMode(t *testing.T) {
	good := map[string]uint32{
		"644": 0o644, "0755": 0o755, "7": 0o7, "4755": 0o4755,
		"rw-r--r--": 0o644, "-rwxr-xr-x": 0o755, "rwsr-xr-x": 0o4755, "rwxrwxrwt": 0o1777, "rwxr-sr-x": 0o2755,
	}
	for in, want := range good {
		got, ok := parseRemoteMode(in)
		if !ok || got != want {
			t.Errorf("parseRemoteMode(%q) = %o, %v; want %o", in, got, ok, want)
		}
	}
	for _, in := range []string{"", "888", "77777", "rw-r--r-", "rwxrwxrwz", "xwr------", "rwxrwxrws"} {
		if _, ok := parseRemoteMode(in); ok {
			t.Errorf("parseRemoteMode(%q) accepted", in)
		}
	}
}

func TestPartialPaths(t *testing.T) {
	if got := partialRemotePath("/etc/enigma2/bouquets.tv", "abc"); got != "/etc/enigma2/.bouquets.tv.dcclite-abc.part" {
		t.Errorf("remote partial = %q", got)
	}
	got := partialDownloadPath(filepath.Join("dir", "file.bin"), "abc")
	if want := filepath.Join("dir", ".file.bin.dcclite-abc.part"); got != want {
		t.Errorf("local partial = %q, want %q", got, want)
	}
}

func TestEstimateTransferProgress(t *testing.T) {
	t0 := time.Now()
	p, speed, bytes, at := estimateTransferProgress(core.TransferProgress{BytesDone: 0, BytesTotal: 1000}, 0, time.Time{}, 0, t0)
	if speed != 0 || p.SpeedBytesPerSecond != 0 || !at.Equal(t0) || bytes != 0 {
		t.Fatal("first sample must only establish the baseline")
	}
	p, speed, _, _ = estimateTransferProgress(core.TransferProgress{BytesDone: 500, BytesTotal: 1000}, 0, t0, 0, t0.Add(time.Second))
	if speed != 500 || p.SpeedBytesPerSecond != 500 || p.ETASeconds != 1 {
		t.Fatalf("speed=%v progress=%+v", speed, p)
	}
	p, speed, _, _ = estimateTransferProgress(core.TransferProgress{BytesDone: 100, BytesTotal: 1000}, 500, t0, 500, t0.Add(time.Second))
	if speed != 0 || p.ETASeconds != 0 {
		t.Fatal("a restarted transfer must reset the speed estimate")
	}
}
