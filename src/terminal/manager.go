// Package terminal runs the Telnet terminal sessions behind the browser tabs.
package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/core"
)

var (
	ErrSessionLimit    = errors.New("terminal session limit reached")
	ErrSessionNotFound = errors.New("terminal session not found")
	ErrSessionAttached = errors.New("terminal session is already attached")
)

const (
	maxSessions          = 16
	maxSessionsPerDevice = 8
	unattachedSessionTTL = 45 * time.Second
	// detachedSessionTTL is how long a shell is kept after its browser
	// connection dropped, so the page can reconnect and carry on.
	detachedSessionTTL = 10 * time.Minute
)

type SessionInfo struct {
	ID        string    `json:"id"`
	ProfileID string    `json:"profile_id"`
	Protocol  string    `json:"protocol"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	OpenedAt  time.Time `json:"opened_at"`
}

type managedSession struct {
	info       SessionInfo
	backend    core.TerminalSession
	pump       *outputPump
	attachment *Attachment
	attached   bool
	expiresAt  time.Time
}

type Manager struct {
	mu       sync.Mutex
	sessions map[string]*managedSession
	opening  map[string]int
	stop     chan struct{}
	closed   chan struct{}
	once     sync.Once
}

func NewManager() *Manager {
	m := &Manager{sessions: make(map[string]*managedSession), opening: make(map[string]int), stop: make(chan struct{}), closed: make(chan struct{})}
	go m.cleanupLoop()
	return m
}

func ValidateDimensions(columns, rows int) error {
	if columns < 20 || columns > 240 || rows < 5 || rows > 100 {
		return errors.New("terminal size must be 20-240 columns and 5-100 rows")
	}
	return nil
}

func (m *Manager) Open(ctx context.Context, profile config.Profile, protocol string, port, columns, rows int) (SessionInfo, error) {
	if err := validateHostPort(profile, port); err != nil {
		return SessionInfo{}, err
	}
	if err := ValidateDimensions(columns, rows); err != nil {
		return SessionInfo{}, err
	}
	protocol = strings.ToLower(strings.TrimSpace(protocol))
	if protocol != "telnet" {
		return SessionInfo{}, errors.New("terminal protocol must be telnet")
	}

	m.mu.Lock()
	count := len(m.sessions)
	deviceCount := m.opening[profile.ID]
	for _, session := range m.sessions {
		if session.info.ProfileID == profile.ID {
			deviceCount++
		}
	}
	if count+totalOpening(m.opening) >= maxSessions || deviceCount >= maxSessionsPerDevice {
		m.mu.Unlock()
		return SessionInfo{}, ErrSessionLimit
	}
	m.opening[profile.ID]++
	m.mu.Unlock()
	defer func() {
		m.mu.Lock()
		m.opening[profile.ID]--
		if m.opening[profile.ID] <= 0 {
			delete(m.opening, profile.ID)
		}
		m.mu.Unlock()
	}()

	var backend core.TerminalSession
	var err error
	switch protocol {
	case "telnet":
		address := net.JoinHostPort(strings.Trim(profile.Host, "[]"), strconv.Itoa(port))
		var conn net.Conn
		conn, err = (&net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}).DialContext(ctx, "tcp", address)
		if err == nil {
			username := profile.Username
			if username == "" {
				username = config.DefaultUsername
			}
			backend = NewTelnetSession(conn, columns, rows, username, profile.Password)
		}
	}
	if err != nil {
		return SessionInfo{}, err
	}
	id, err := newSessionID()
	if err != nil {
		_ = backend.Close()
		return SessionInfo{}, err
	}
	info := SessionInfo{ID: id, ProfileID: profile.ID, Protocol: protocol, Host: profile.Host, Port: port, OpenedAt: time.Now().UTC()}
	m.mu.Lock()
	m.sessions[id] = &managedSession{info: info, backend: backend, pump: newOutputPump(backend), expiresAt: time.Now().Add(unattachedSessionTTL)}
	m.mu.Unlock()
	return info, nil
}

func (m *Manager) SetInitialDirectory(sessionID, directory string) error {
	return m.SetInitialDirectoryAndCommand(sessionID, directory, "pwd")
}

// SetInitialDirectoryAndCommand queues a single command after Telnet login. The directory is always shell-quoted.
func (m *Manager) SetInitialDirectoryAndCommand(sessionID, directory, command string) error {
	if len(directory) > 4096 || strings.ContainsAny(directory, "\r\n\x00") {
		return errors.New("remote directory contains unsupported characters or exceeds 4096 bytes")
	}
	if len(command) > 8192 || strings.ContainsAny(command, "\r\n\x00") {
		return errors.New("initial command contains unsupported characters or exceeds 8192 bytes")
	}
	command = strings.TrimSpace(command)
	if directory != "" {
		prefix := "cd " + shellQuote(directory)
		if command == "" {
			command = prefix + " && pwd"
		} else {
			command = prefix + " && " + command
		}
	}
	if command == "" {
		return nil
	}
	payload := append([]byte(command), '\r')
	m.mu.Lock()
	session, ok := m.sessions[sessionID]
	m.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	if telnet, ok := session.backend.(*TelnetSession); ok {
		return telnet.SetInitialCommand(payload)
	}
	_, err := session.backend.Write(payload)
	return err
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}

// Attach connects a browser to a session from the start of its buffered output.
func (m *Manager) Attach(id string) (core.TerminalSession, SessionInfo, error) {
	attachment, info, err := m.AttachAt(id, 0)
	if err != nil {
		return nil, SessionInfo{}, err
	}
	return attachment, info, nil
}

// AttachAt connects a browser to a session and continues its output at the
// given absolute byte offset (0 for a new page). A browser that reconnects after
// a dropped connection may find the old attachment still registered, because the
// server has not noticed the loss yet; the new one replaces it.
func (m *Manager) AttachAt(id string, offset int64) (*Attachment, SessionInfo, error) {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if !ok {
		m.mu.Unlock()
		return nil, SessionInfo{}, ErrSessionNotFound
	}
	if time.Now().After(session.expiresAt) && !session.attached {
		delete(m.sessions, id)
		m.mu.Unlock()
		_ = session.backend.Close()
		return nil, SessionInfo{}, ErrSessionNotFound
	}
	if session.pump == nil {
		session.pump = newOutputPump(session.backend)
	}
	previous := session.attachment
	attachment := newAttachment(session.pump, offset, func(a *Attachment) { m.detach(id, a) })
	session.attachment = attachment
	session.attached = true
	info := session.info
	m.mu.Unlock()
	if previous != nil {
		_ = previous.Close()
	}
	return attachment, info, nil
}

// Info returns the details of a live session, for a page that wants to know
// whether it can still reconnect to it.
func (m *Manager) Info(id string) (SessionInfo, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok {
		return SessionInfo{}, false
	}
	return session.info, true
}

// detach records that a browser left. The shell stays for detachedSessionTTL.
func (m *Manager) detach(id string, a *Attachment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[id]
	if !ok || session.attachment != a {
		return
	}
	session.attachment = nil
	session.attached = false
	session.expiresAt = time.Now().Add(detachedSessionTTL)
}

func (m *Manager) CloseProfile(profileID string) {
	m.mu.Lock()
	var matches []*managedSession
	for id, session := range m.sessions {
		if session.info.ProfileID == profileID {
			matches = append(matches, session)
			delete(m.sessions, id)
		}
	}
	m.mu.Unlock()
	for _, session := range matches {
		_ = session.backend.Close()
	}
}

func (m *Manager) Close(id string) error {
	m.mu.Lock()
	session, ok := m.sessions[id]
	if ok {
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	if !ok {
		return ErrSessionNotFound
	}
	return session.backend.Close()
}

func (m *Manager) CloseAll() {
	m.once.Do(func() { close(m.stop) })
	<-m.closed
	m.mu.Lock()
	all := make([]*managedSession, 0, len(m.sessions))
	for id, session := range m.sessions {
		all = append(all, session)
		delete(m.sessions, id)
	}
	m.mu.Unlock()
	for _, session := range all {
		_ = session.backend.Close()
	}
}

func (m *Manager) cleanupLoop() {
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	defer close(m.closed)
	for {
		select {
		case <-m.stop:
			return
		case now := <-ticker.C:
			m.mu.Lock()
			var expired []*managedSession
			for id, session := range m.sessions {
				if !session.attached && now.After(session.expiresAt) {
					expired = append(expired, session)
					delete(m.sessions, id)
				}
			}
			m.mu.Unlock()
			for _, session := range expired {
				_ = session.backend.Close()
			}
		}
	}
}

func totalOpening(opening map[string]int) int {
	total := 0
	for _, count := range opening {
		total += count
	}
	return total
}

func newSessionID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", fmt.Errorf("generate terminal session id: %w", err)
	}
	return hex.EncodeToString(value[:]), nil
}

func validateHostPort(profile config.Profile, port int) error {
	if err := profile.Validate(); err != nil {
		return err
	}
	if !profile.Enabled {
		return errors.New("device profile is disabled")
	}
	if port < 1 || port > 65535 {
		return errors.New("port must be between 1 and 65535")
	}
	return nil
}
