package filetransfer

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/core"
	"github.com/didou07/dcc-lite/src/logx"
)

var flog = logx.For("files")

var (
	ErrSessionNotFound = errors.New("file transfer session not found")
	ErrSessionLimit    = errors.New("Too many file connections are busy with transfers.")
	ErrTaskNotFound    = errors.New("transfer task not found")
	ErrPathNotFound    = errors.New("Folder not found on the device")
)

const (
	maxFileSessions          = 8
	maxFileSessionsPerDevice = 2
	maxTransferTasks         = 100
	idleSessionTTL           = 2 * time.Hour
	maxDirectoryEntries      = 100000
	maxDirectoryDepth        = 64
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
	info    SessionInfo
	adapter core.FileTransfer
	mu      sync.Mutex
	closed  bool

	lastUsed time.Time
}

type Manager struct {
	mu               sync.Mutex
	sessions         map[string]*managedSession
	tasks            map[string]*taskEntry
	queue            chan struct{}
	opening          int
	openingByProfile map[string]int
	closed           bool
	stop             chan struct{}
	reaperDone       chan struct{}
	closeOnce        sync.Once
	openWG           sync.WaitGroup
	shutdownCtx      context.Context
	shutdownCancel   context.CancelFunc
}

func NewManager() *Manager {
	shutdownCtx, shutdownCancel := context.WithCancel(context.Background())
	m := &Manager{sessions: make(map[string]*managedSession), tasks: make(map[string]*taskEntry), queue: make(chan struct{}, 2), openingByProfile: make(map[string]int), stop: make(chan struct{}), reaperDone: make(chan struct{}), shutdownCtx: shutdownCtx, shutdownCancel: shutdownCancel}
	go m.reapIdleLoop()
	return m
}

func (m *Manager) Open(ctx context.Context, profile config.Profile, protocol string, port int, password string) (SessionInfo, error) {
	if err := profile.Validate(); err != nil {
		return SessionInfo{}, err
	}
	if !profile.Enabled {
		return SessionInfo{}, errors.New("device profile is disabled")
	}
	if port < 1 || port > 65535 {
		return SessionInfo{}, errors.New("port must be between 1 and 65535")
	}
	protocol = normalizeProtocol(protocol)
	if protocol != "ftp" {
		return SessionInfo{}, errors.New("file protocol must be FTP")
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return SessionInfo{}, errors.New("file transfer manager is closed")
	}

	var evicted []*managedSession
	for {
		count := m.openingByProfile[profile.ID]
		for _, session := range m.sessions {
			if session.info.ProfileID == profile.ID {
				count++
			}
		}
		deviceFull := count >= maxFileSessionsPerDevice
		if len(m.sessions)+m.opening < maxFileSessions && !deviceFull {
			break
		}
		victim := m.idleSessionLocked(profile.ID, deviceFull)
		if victim == nil {
			m.mu.Unlock()
			go closeSessions(evicted)
			return SessionInfo{}, ErrSessionLimit
		}
		delete(m.sessions, victim.info.ID)
		m.cancelSessionTasksLocked(victim.info.ID)
		evicted = append(evicted, victim)
	}
	m.opening++
	m.openingByProfile[profile.ID]++
	m.openWG.Add(1)
	m.mu.Unlock()
	defer m.openWG.Done()
	dialCtx, cancelDial := context.WithCancel(ctx)
	stopShutdown := context.AfterFunc(m.shutdownCtx, cancelDial)
	defer func() {
		stopShutdown()
		cancelDial()
	}()
	if len(evicted) > 0 {
		go closeSessions(evicted)
	}
	reservationHeld := true
	releaseReservation := func() {
		if !reservationHeld {
			return
		}
		m.mu.Lock()
		m.opening--
		m.openingByProfile[profile.ID]--
		if m.openingByProfile[profile.ID] == 0 {
			delete(m.openingByProfile, profile.ID)
		}
		m.mu.Unlock()
		reservationHeld = false
	}
	defer releaseReservation()
	var adapter core.FileTransfer
	var err error
	switch protocol {
	case "ftp":
		adapter, err = OpenResilientFTP(dialCtx, FTPOptions{Host: profile.Host, Port: port, Username: profile.Username, Password: password})
	}
	if err != nil {
		flog.Warn("file session failed to open", "device", profile.Name, "host", profile.Host, "port", port, "error", err)
		return SessionInfo{}, friendlyConnectError(profile.Host, port, err)
	}
	id, err := newFileID()
	if err != nil {
		_ = adapter.Close()
		return SessionInfo{}, err
	}
	info := SessionInfo{ID: id, ProfileID: profile.ID, Protocol: protocol, Host: profile.Host, Port: port, OpenedAt: time.Now().UTC()}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		_ = adapter.Close()
		return SessionInfo{}, errors.New("file transfer manager is closed")
	}
	m.opening--
	m.openingByProfile[profile.ID]--
	if m.openingByProfile[profile.ID] == 0 {
		delete(m.openingByProfile, profile.ID)
	}
	reservationHeld = false
	m.sessions[id] = &managedSession{info: info, adapter: adapter, lastUsed: time.Now()}
	m.mu.Unlock()
	flog.Info("file session opened", "device", profile.Name, "host", profile.Host, "port", port, "session", id)
	return info, nil
}

func normalizeProtocol(value string) string {
	if value == "" {
		return ""
	}
	switch value {
	case "FTP", "ftp":
		return "ftp"
	}
	return value
}

func newFileID() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func friendlyConnectError(host string, port int, err error) error {
	lower := strings.ToLower(err.Error())
	at := host + ":" + strconv.Itoa(port)
	switch {
	case strings.Contains(lower, "login failed"):
		return errors.New("The receiver rejected the user name or password for FTP.")
	case strings.Contains(lower, "refused"):
		return fmt.Errorf("Nothing answers for FTP at %s.", at)
	case strings.Contains(lower, "no route to host") || strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "no such host"):
		return fmt.Errorf("Cannot reach %s.", at)
	case strings.Contains(lower, "timeout") || strings.Contains(lower, "deadline exceeded"):
		return fmt.Errorf("The receiver at %s did not reply in time for FTP.", at)
	case strings.Contains(lower, "reset by peer") || strings.Contains(lower, "eof"):
		return fmt.Errorf("The receiver closed the FTP connection at %s.", at)
	}
	return err
}
