package terminal

import (
	"testing"
	"time"
)

// A shell that is open in a page is never closed to make room; the one detached the longest goes first.
func TestDetachedSessionLockedSkipsAttachedAndPicksOldest(t *testing.T) {
	now := time.Now()
	manager := &Manager{sessions: map[string]*managedSession{}}
	manager.sessions["live"] = &managedSession{info: SessionInfo{ID: "live", ProfileID: "a"}, attached: true, expiresAt: now.Add(-time.Hour)}
	manager.sessions["new"] = &managedSession{info: SessionInfo{ID: "new", ProfileID: "a"}, expiresAt: now.Add(2 * time.Hour)}
	manager.sessions["old"] = &managedSession{info: SessionInfo{ID: "old", ProfileID: "a"}, expiresAt: now.Add(time.Hour)}
	manager.sessions["other"] = &managedSession{info: SessionInfo{ID: "other", ProfileID: "b"}, expiresAt: now.Add(10 * time.Minute)}

	manager.mu.Lock()
	defer manager.mu.Unlock()
	if id, victim := manager.detachedSessionLocked("a", true); victim == nil || id != "old" {
		t.Fatalf("per-device victim = %q, want \"old\"", id)
	}
	if id, victim := manager.detachedSessionLocked("a", false); victim == nil || id != "other" {
		t.Fatalf("global victim = %q, want \"other\"", id)
	}
	delete(manager.sessions, "new")
	delete(manager.sessions, "old")
	delete(manager.sessions, "other")
	if _, victim := manager.detachedSessionLocked("a", true); victim != nil {
		t.Fatal("an attached shell must never be chosen")
	}
}
