package filetransfer

import (
	"sort"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

func (m *Manager) withAdapter(id string, fn func(core.FileTransfer) error) error {
	session, err := m.get(id)
	if err != nil {
		return err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return ErrSessionNotFound
	}
	return fn(session.adapter)
}

func (m *Manager) get(id string) (*managedSession, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return nil, ErrSessionNotFound
	}
	session.lastUsed = time.Now()
	return session, nil
}

func (m *Manager) idleSessionLocked(profileID string, deviceOnly bool) *managedSession {
	busy, latest := m.sessionUsageLocked()
	var victim *managedSession
	var victimTime time.Time
	for id, session := range m.sessions {
		if busy[id] || (deviceOnly && session.info.ProfileID != profileID) {
			continue
		}
		used := lastUse(session, latest[id])
		if victim == nil || used.Before(victimTime) {
			victim = session
			victimTime = used
		}
	}
	return victim
}

func (m *Manager) sessionUsageLocked() (map[string]bool, map[string]time.Time) {
	busy := make(map[string]bool)
	latest := make(map[string]time.Time)
	for _, task := range m.tasks {
		id := task.info.SessionID
		switch task.info.State {
		case "queued", "running":
			busy[id] = true
		}
		if task.info.UpdatedAt.After(latest[id]) {
			latest[id] = task.info.UpdatedAt
		}
	}
	return busy, latest
}

func (m *Manager) cancelSessionTasksLocked(sessionID string) {
	for _, task := range m.tasks {
		if task.info.SessionID == sessionID && (task.info.State == "queued" || task.info.State == "running") {
			task.cancel()
		}
	}
}

func lastUse(session *managedSession, lastTask time.Time) time.Time {
	used := session.lastUsed
	if used.IsZero() {
		used = session.info.OpenedAt
	}
	if lastTask.After(used) {
		used = lastTask
	}
	return used
}

func (m *Manager) reapIdleLoop() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	defer close(m.reaperDone)
	for {
		select {
		case <-m.stop:
			return
		case <-ticker.C:
			m.mu.Lock()
			if m.closed {
				m.mu.Unlock()
				return
			}
			busy, latest := m.sessionUsageLocked()
			now := time.Now()
			var stale []*managedSession
			for id, session := range m.sessions {
				if !busy[id] && now.Sub(lastUse(session, latest[id])) > idleSessionTTL {
					stale = append(stale, session)
					delete(m.sessions, id)
				}
			}
			m.mu.Unlock()
			if len(stale) > 0 {
				flog.Debug("closing idle file sessions", "count", len(stale))
			}
			closeSessions(stale)
		}
	}
}

func closeSessions(sessions []*managedSession) {
	for _, session := range sessions {
		session.mu.Lock()
		session.closed = true
		_ = session.adapter.Close()
		session.mu.Unlock()
	}
}

func (m *Manager) Close(id string) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
		for _, task := range m.tasks {
			if task.info.SessionID == id && (task.info.State == "queued" || task.info.State == "running") {
				task.cancel()
			}
		}
	}
	m.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	session.closed = true
	return session.adapter.Close()
}

func (m *Manager) CloseProfile(profileID string) {
	m.mu.Lock()
	ids := make([]string, 0)
	for id, session := range m.sessions {
		if session.info.ProfileID == profileID {
			ids = append(ids, id)
		}
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Close(id)
	}
}

func (m *Manager) CloseAll() {
	m.mu.Lock()
	m.closed = true
	for _, task := range m.tasks {
		if task.info.State == "queued" || task.info.State == "running" {
			task.cancel()
		}
	}
	m.mu.Unlock()
	m.shutdownCancel()
	m.closeOnce.Do(func() { close(m.stop) })
	<-m.reaperDone
	m.openWG.Wait()
	m.mu.Lock()
	ids := make([]string, 0, len(m.sessions))
	for id := range m.sessions {
		ids = append(ids, id)
	}
	m.mu.Unlock()
	for _, id := range ids {
		_ = m.Close(id)
	}
}

func (m *Manager) Sessions() []SessionInfo {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SessionInfo, 0, len(m.sessions))
	for _, session := range m.sessions {
		out = append(out, session.info)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].OpenedAt.Before(out[j].OpenedAt) })
	return out
}
