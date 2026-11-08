package filetransfer

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSweepStagedKeepsLiveWorkAndRemovesTheRest(t *testing.T) {
	staging, err := NewLocalFSAt(filepath.Join(t.TempDir(), "staging"))
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-2 * time.Hour)
	make1 := func(id string, modified time.Time) string {
		dir := filepath.Join(staging.Root(), id)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		file := filepath.Join(dir, "f.bin")
		if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		_ = os.Chtimes(file, modified, modified)
		_ = os.Chtimes(dir, modified, modified)
		return dir
	}
	orphanOld := make1("orphan-old", old)
	orphanFresh := make1("orphan-fresh", time.Now()) // an upload that is still arriving
	running := make1("running", old)
	failed := make1("failed", old)

	m := NewManager()
	m.tasks["a"] = &taskEntry{info: TaskInfo{ID: "a", Staged: true, Direction: "upload", LocalPath: "running/f.bin", State: "running", UpdatedAt: old}}
	m.tasks["b"] = &taskEntry{info: TaskInfo{ID: "b", Staged: true, Direction: "upload", LocalPath: "failed/f.bin", State: "failed", UpdatedAt: time.Now().Add(-time.Hour)}}
	m.SweepStaged(staging, time.Now())

	for dir, wantKept := range map[string]bool{orphanOld: false, orphanFresh: true, running: true, failed: true} {
		_, err := os.Stat(dir)
		if (err == nil) != wantKept {
			t.Errorf("%s kept=%v, want %v", filepath.Base(dir), err == nil, wantKept)
		}
	}

	// A failed transfer that nobody resumed for hours is dropped.
	m.tasks["b"].info.UpdatedAt = time.Now().Add(-7 * time.Hour)
	m.SweepStaged(staging, time.Now())
	if _, err := os.Stat(failed); err == nil {
		t.Error("a stale failed upload must be removed")
	}
}

func TestPruneMakesRoomByDroppingOldCompletedTasks(t *testing.T) {
	m := NewManager()
	now := time.Now()
	for i := 0; i < maxTransferTasks; i++ {
		id := string(rune('a'+i%26)) + string(rune('A'+i/26))
		m.tasks[id] = &taskEntry{info: TaskInfo{ID: id, State: "completed", UpdatedAt: now.Add(-time.Duration(i) * time.Second)}}
	}
	m.tasks["live"] = &taskEntry{info: TaskInfo{ID: "live", State: "running", UpdatedAt: now}}
	m.mu.Lock()
	removed := m.pruneTasksLocked()
	left := len(m.tasks)
	m.mu.Unlock()
	if len(removed) == 0 || left >= maxTransferTasks {
		t.Fatalf("pruned %d, %d left; a full list of finished transfers must make room", len(removed), left)
	}
	if _, ok := m.tasks["live"]; !ok {
		t.Fatal("a running transfer must never be pruned")
	}
}
