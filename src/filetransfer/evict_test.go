package filetransfer

import (
	"testing"
	"time"
)

// When a limit is reached, the least recently used session without a running transfer is the one to close.
func TestIdleSessionLockedPicksLeastRecentlyUsedAndSkipsBusy(t *testing.T) {
	now := time.Now()
	manager := &Manager{sessions: map[string]*managedSession{}, tasks: map[string]*taskEntry{}}
	manager.sessions["old"] = &managedSession{info: SessionInfo{ID: "old", ProfileID: "a"}, lastUsed: now.Add(-3 * time.Hour)}
	manager.sessions["busy"] = &managedSession{info: SessionInfo{ID: "busy", ProfileID: "a"}, lastUsed: now.Add(-5 * time.Hour)}
	manager.sessions["other"] = &managedSession{info: SessionInfo{ID: "other", ProfileID: "b"}, lastUsed: now.Add(-9 * time.Hour)}
	manager.tasks["t"] = &taskEntry{info: TaskInfo{ID: "t", SessionID: "busy", State: "running", UpdatedAt: now}}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if got := manager.idleSessionLocked("a", true); got == nil || got.info.ID != "old" {
		t.Fatalf("per-device victim = %+v, want the idle session \"old\" (never the busy one)", got)
	}
	if got := manager.idleSessionLocked("a", false); got == nil || got.info.ID != "other" {
		t.Fatalf("global victim = %+v, want the least recently used idle session \"other\"", got)
	}
	// A transfer that just finished counts as recent use, so "old" is now the least recently used session.
	manager.tasks["t"].info.State = "completed"
	if got := manager.idleSessionLocked("a", true); got == nil || got.info.ID != "old" {
		t.Fatalf("after the transfer finished, victim = %+v, want \"old\" (the finished transfer counts as recent use)", got)
	}
	// Once that transfer is long finished, the session it ran in is the least recently used one again.
	manager.tasks["t"].info.UpdatedAt = now.Add(-6 * time.Hour)
	if got := manager.idleSessionLocked("a", true); got == nil || got.info.ID != "busy" {
		t.Fatalf("with an old finished transfer, victim = %+v, want \"busy\"", got)
	}
}

func TestIdleSessionLockedReturnsNilWhenEverythingIsBusy(t *testing.T) {
	manager := &Manager{sessions: map[string]*managedSession{}, tasks: map[string]*taskEntry{}}
	manager.sessions["s"] = &managedSession{info: SessionInfo{ID: "s", ProfileID: "a"}, lastUsed: time.Now()}
	manager.tasks["t"] = &taskEntry{info: TaskInfo{ID: "t", SessionID: "s", State: "queued", UpdatedAt: time.Now()}}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if got := manager.idleSessionLocked("a", true); got != nil {
		t.Fatalf("victim = %+v, want nil", got)
	}
}
