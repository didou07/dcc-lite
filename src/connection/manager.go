// Package connection probes whether a saved device answers on the services
// DCC Lite uses: FTP and Telnet.
package connection

import (
	"context"
	"net"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/config"
)

// Service describes the result of one TCP probe.
type Service struct {
	Name      string `json:"name"`
	Port      int    `json:"port"`
	Open      bool   `json:"open"`
	LatencyMS int64  `json:"latency_ms,omitempty"`
	Error     string `json:"error,omitempty"`
}

// Status is the last known reachability of a saved device.
type Status struct {
	ProfileID string    `json:"profile_id"`
	State     string    `json:"state"` // checking, online, partial, offline, error
	Message   string    `json:"message"`
	Services  []Service `json:"services,omitempty"`
	CheckedAt time.Time `json:"checked_at,omitempty"`
}

type Checker interface {
	Check(context.Context, config.Profile) Status
}

type Manager struct {
	mu       sync.RWMutex
	checker  Checker
	statuses map[string]Status
	active   map[string]context.CancelFunc
	sequence map[string]uint64
}

func NewManager(checker Checker) *Manager {
	return &Manager{
		checker: checker, statuses: make(map[string]Status),
		active: make(map[string]context.CancelFunc), sequence: make(map[string]uint64),
	}
}

func NewTCPChecker() *TCPChecker { return &TCPChecker{Timeout: 3 * time.Second} }

// Test probes the device and stores the newest result. A newer test for the
// same profile cancels the older one so a slow probe cannot overwrite fresh data.
func (m *Manager) Test(ctx context.Context, profile config.Profile) Status {
	m.mu.Lock()
	if previous := m.active[profile.ID]; previous != nil {
		previous()
	}
	checkCtx, cancel := context.WithTimeout(ctx, 6*time.Second)
	m.sequence[profile.ID]++
	sequence := m.sequence[profile.ID]
	m.active[profile.ID] = cancel
	m.statuses[profile.ID] = Status{ProfileID: profile.ID, State: "checking", Message: "Checking device"}
	m.mu.Unlock()

	status := m.checker.Check(checkCtx, profile)
	cancel()
	m.mu.Lock()
	if m.sequence[profile.ID] == sequence {
		delete(m.active, profile.ID)
		m.statuses[profile.ID] = status
	}
	m.mu.Unlock()
	return status
}

func (m *Manager) List() []Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Status, 0, len(m.statuses))
	for _, s := range m.statuses {
		out = append(out, s)
	}
	return out
}

func (m *Manager) Get(profileID string) (Status, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.statuses[profileID]
	return s, ok
}

func (m *Manager) Forget(profileID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cancel := m.active[profileID]; cancel != nil {
		cancel()
	}
	delete(m.active, profileID)
	delete(m.statuses, profileID)
	m.sequence[profileID]++
}

// TCPChecker opens a plain TCP connection to the FTP and Telnet ports.
type TCPChecker struct{ Timeout time.Duration }

func (c *TCPChecker) Check(parent context.Context, profile config.Profile) Status {
	status := Status{ProfileID: profile.ID, State: "offline", Message: "Device is not reachable"}
	if err := profile.Validate(); err != nil {
		status.State, status.Message = "error", err.Error()
		return status
	}
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	targets := []Service{
		{Name: "FTP", Port: profile.EffectiveFTPPort()},
		{Name: "Telnet", Port: profile.EffectiveTelnetPort()},
	}
	results := make([]Service, len(targets))
	var wg sync.WaitGroup
	for i, target := range targets {
		wg.Add(1)
		go func(i int, target Service) {
			defer wg.Done()
			start := time.Now()
			dialer := net.Dialer{Timeout: timeout}
			conn, err := dialer.DialContext(parent, "tcp", net.JoinHostPort(strings.Trim(profile.Host, "[]"), strconv.Itoa(target.Port)))
			if err != nil {
				target.Error = classifyNetworkError(err)
			} else {
				_ = conn.Close()
				target.Open = true
				target.LatencyMS = time.Since(start).Milliseconds()
			}
			results[i] = target
		}(i, target)
	}
	wg.Wait()
	status.Services = results
	status.CheckedAt = time.Now().UTC()
	open := 0
	var names []string
	for _, s := range results {
		if s.Open {
			open++
			names = append(names, s.Name)
		}
	}
	switch {
	case parent.Err() != nil:
		status.State, status.Message = "offline", "Check was cancelled"
	case open == len(results):
		status.State, status.Message = "online", "FTP and Telnet are reachable"
	case open > 0:
		status.State, status.Message = "partial", "Reachable: "+strings.Join(names, ", ")
	default:
		status.State, status.Message = "offline", results[0].Error
		if status.Message == "" {
			status.Message = "No service answered"
		}
	}
	return status
}

func classifyNetworkError(err error) string {
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "no such host"):
		return "Host name could not be resolved"
	case strings.Contains(message, "connection refused"):
		return "Connection refused"
	case strings.Contains(message, "network is unreachable"):
		return "Network is unreachable"
	case strings.Contains(message, "timeout"), strings.Contains(message, "deadline"):
		return "Connection timed out"
	case strings.Contains(message, "canceled"), strings.Contains(message, "cancelled"):
		return "Cancelled"
	}
	return "Connection failed"
}
