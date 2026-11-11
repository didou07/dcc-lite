package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/httpapi"
	"github.com/didou07/dcc-lite/src/remoteaccess"
)

const (
	defaultRemoteListen = config.DefaultRemoteListen
	maxTLSHosts         = 10
)

var tlsHostPattern = regexp.MustCompile(`^[A-Za-z0-9.\-:\[\]]{1,255}$`)

type remoteManager struct {
	application *app.App
	store       *config.ProfileStore
	dataDir     string
	loopback    string

	mu       sync.Mutex
	settings app.RemoteSettings
	cli      bool
	certFile string
	keyFile  string

	server   *http.Server
	closeAll func()
	plan     remotePlan
	running  bool
	hosts    []string
	port     string
	lastErr  string
}

func newRemoteManager(application *app.App, store *config.ProfileStore, dataDir, loopback string) *remoteManager {
	return &remoteManager{
		application: application, store: store, dataDir: dataDir, loopback: loopback,
		settings: app.RemoteSettings{Listen: defaultRemoteListen},
	}
}

func (m *remoteManager) Init(flags remoteSettings) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if flags.Listen != "" {
		m.cli = true
		m.certFile, m.keyFile = flags.CertFile, flags.KeyFile
		m.settings = app.RemoteSettings{Enabled: true, Listen: flags.Listen, TLSHosts: flags.ExtraHost, PlainHTTP: flags.Plain}
		plan, err := m.start()
		if err != nil {
			return err
		}
		if plan.NewPassword != "" {
			fmt.Fprintf(os.Stderr, "\nRemote access password (shown once, change it with --set-remote-password): %s\n\n", plan.NewPassword)
		}
		return nil
	}
	saved, err := m.load()
	if err != nil {
		slog.Warn("could not read saved remote access settings", "error", err)
		return nil
	}
	m.settings = saved
	if saved.Enabled {
		plan, err := m.start()
		if err != nil {
			m.lastErr = err.Error()
			slog.Warn("remote access could not start", "error", err)
			return nil
		}
		if plan.NewPassword != "" {
			fmt.Fprintf(os.Stderr, "\nRemote access password (shown once): %s\n\n", plan.NewPassword)
		}
	}
	return nil
}

func (m *remoteManager) load() (app.RemoteSettings, error) {
	r := m.store.Remote()
	return app.RemoteSettings{Enabled: r.Enabled, Listen: r.EffectiveListen(), TLSHosts: r.TLSHosts, PlainHTTP: r.PlainHTTP}, nil
}

func (m *remoteManager) save() error {
	r := m.store.Remote()
	r.Enabled, r.Listen, r.TLSHosts, r.PlainHTTP = m.settings.Enabled, m.settings.Listen, m.settings.TLSHosts, m.settings.PlainHTTP
	return m.store.SaveRemote(r)
}

func (m *remoteManager) normalize(in app.RemoteSettings) (app.RemoteSettings, error) {
	out := app.RemoteSettings{Enabled: in.Enabled, Listen: strings.TrimSpace(in.Listen), PlainHTTP: in.PlainHTTP}
	if out.Listen == "" {
		out.Listen = defaultRemoteListen
	}
	if err := validateRemoteListen(out.Listen, m.loopback); err != nil {
		return out, err
	}
	var hosts []string
	for _, h := range strings.Split(in.TLSHosts, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if !tlsHostPattern.MatchString(h) {
			return out, fmt.Errorf("%q is not a valid host name or IP address", h)
		}
		hosts = append(hosts, h)
	}
	if len(hosts) > maxTLSHosts {
		return out, fmt.Errorf("at most %d names or addresses are allowed", maxTLSHosts)
	}
	out.TLSHosts = strings.Join(hosts, ", ")
	return out, nil
}

func (m *remoteManager) start() (remotePlan, error) {
	plan, err := prepareRemote(m.dataDir, m.store, remoteSettings{
		Listen: m.settings.Listen, CertFile: m.certFile, KeyFile: m.keyFile, ExtraHost: m.settings.TLSHosts, Plain: m.settings.PlainHTTP,
	})
	if err != nil {
		return remotePlan{}, fmt.Errorf("remote access setup: %w", err)
	}
	raw, err := net.Listen("tcp", m.settings.Listen)
	if err != nil {
		return remotePlan{}, fmt.Errorf("cannot listen on %s: %w", m.settings.Listen, err)
	}
	server := &http.Server{
		Handler:           httpapi.NewRemote(m.application, httpapi.RemoteOptions{Auth: plan.Auth}),
		ErrorLog:          log.New(tlsNoiseFilter{}, "", 0),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	conns := map[net.Conn]struct{}{}
	var connsMu sync.Mutex
	server.ConnState = func(c net.Conn, state http.ConnState) {
		connsMu.Lock()
		defer connsMu.Unlock()
		switch state {
		case http.StateNew, http.StateHijacked:
			conns[c] = struct{}{}
		case http.StateClosed:
			delete(conns, c)
		}
	}
	m.server, m.plan, m.running = server, plan, true
	m.closeAll = func() {
		connsMu.Lock()
		defer connsMu.Unlock()
		for c := range conns {
			_ = c.Close()
		}
	}
	var listener net.Listener = raw
	if !m.settings.PlainHTTP {
		listener = tls.NewListener(raw, plan.Certificate.ServerConfig())
	}
	_, m.port, _ = net.SplitHostPort(raw.Addr().String())
	m.lastErr = ""
	m.hosts = m.linkHosts(plan)
	if m.settings.PlainHTTP {
		slog.Warn("Remote access (plain HTTP) is ready: NOT ENCRYPTED, the password and all data can be read on the network", "port", m.port)
	} else {
		slog.Info("Remote access (HTTPS) is ready", "port", m.port, "certificate", plan.Certificate.Source, "sha256", plan.Certificate.Fingerprint)
	}
	for _, url := range m.links() {
		slog.Info("Open from another device", "url", url)
	}
	if !m.settings.PlainHTTP && plan.Certificate.Source == "self-signed" {
		slog.Info("Self-signed certificate: the browser will warn once; compare the SHA-256 fingerprint above before accepting")
	}
	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("remote HTTPS server", "error", err)
		}
	}()
	return plan, nil
}

func (m *remoteManager) linkHosts(plan remotePlan) []string {
	var hosts []string
	for _, extra := range strings.Split(m.settings.TLSHosts, ",") {
		if extra = strings.TrimSpace(extra); extra != "" {
			hosts = append(hosts, extra)
		}
	}
	if len(hosts) == 0 && m.cli {
		if !m.settings.PlainHTTP {
			return displayHosts(plan.Certificate.Hosts)
		}
		hosts = remoteaccess.DefaultHosts()
	}
	seen := map[string]bool{}
	var unique []string
	for _, h := range displayHosts(hosts) {
		if !seen[strings.ToLower(h)] {
			seen[strings.ToLower(h)] = true
			unique = append(unique, h)
		}
	}
	return unique
}

func (m *remoteManager) links() []string {
	scheme := "https://"
	if m.settings.PlainHTTP {
		scheme = "http://"
	}
	var urls []string
	for _, host := range m.hosts {
		urls = append(urls, scheme+net.JoinHostPort(host, m.port))
	}
	return urls
}

func (m *remoteManager) stop() {
	if m.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = m.server.Shutdown(ctx)
	_ = m.server.Close()
	if m.closeAll != nil {
		m.closeAll()
	}
	if m.plan.Auth != nil {
		m.plan.Auth.RevokeAll()
	}
	m.server, m.closeAll, m.running, m.plan, m.hosts = nil, nil, false, remotePlan{}, nil
	slog.Info("Remote access (HTTPS) stopped")
}

func (m *remoteManager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.stop()
}

func (m *remoteManager) passwordSet() bool {
	if m.plan.Creds != nil {
		return m.plan.Creds.Configured()
	}
	creds, err := remoteaccess.OpenCredentials(m.store)
	return err == nil && creds.Configured()
}

func (m *remoteManager) status() app.RemoteStatus {
	st := app.RemoteStatus{RemoteSettings: m.settings, Running: m.running, Managed: !m.cli, PasswordSet: m.passwordSet(), Error: m.lastErr}
	if m.running {
		st.Port = m.port
		st.Fingerprint = m.plan.Certificate.Fingerprint
		st.CertSource = m.plan.Certificate.Source
		st.URLs = m.links()
	}
	return st
}

func (m *remoteManager) Status() app.RemoteStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status()
}

func (m *remoteManager) Apply(in app.RemoteSettings) (app.RemoteStatus, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cli {
		return m.status(), "", errors.New("remote access was started with --remote-listen; restart DCC Lite without it to manage it here")
	}
	next, err := m.normalize(in)
	if err != nil {
		return m.status(), "", err
	}
	m.stop()
	previous := m.settings
	m.settings = next
	newPassword := ""
	if next.Enabled {
		plan, err := m.start()
		if err != nil {

			m.settings = previous
			if previous.Enabled {
				_, _ = m.start()
			}
			m.lastErr = err.Error()
			return m.status(), "", err
		}
		newPassword = plan.NewPassword
	} else {
		m.lastErr = ""
	}
	if err := m.save(); err != nil {
		return m.status(), newPassword, fmt.Errorf("the settings are active but could not be saved: %w", err)
	}
	return m.status(), newPassword, nil
}

func (m *remoteManager) credentials() (*remoteaccess.Credentials, error) {
	if m.plan.Creds != nil {
		return m.plan.Creds, nil
	}
	return remoteaccess.OpenCredentials(m.store)
}

func (m *remoteManager) SetPassword(password string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	creds, err := m.credentials()
	if err != nil {
		return err
	}
	if err := creds.Set(password); err != nil {
		return err
	}
	if m.plan.Auth != nil {
		m.plan.Auth.RevokeAll()
	}
	return nil
}

func (m *remoteManager) GeneratePassword() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	creds, err := m.credentials()
	if err != nil {
		return "", err
	}
	password, err := creds.Generate()
	if err != nil {
		return "", err
	}
	if m.plan.Auth != nil {
		m.plan.Auth.RevokeAll()
	}
	return password, nil
}
