package main

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/mail"
	"os"
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
	redirect *http.Server
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
		m.settings = app.RemoteSettings{Enabled: true, Listen: flags.Listen, TLSHosts: flags.ExtraHost, TLSEmail: flags.Email}
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
		remoteLog.Warn("could not read saved remote access settings", "error", err)
		return nil
	}
	saved, err = m.normalize(saved)
	if err != nil {
		m.lastErr = err.Error()
		remoteLog.Warn("saved remote access settings are invalid", "error", err)
		return nil
	}
	m.settings = saved
	if saved.Enabled {
		plan, err := m.start()
		if err != nil {
			m.lastErr = err.Error()
			remoteLog.Warn("remote access could not start", "error", err)
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
	return app.RemoteSettings{Enabled: r.Enabled, Listen: r.EffectiveListen(), TLSHosts: r.TLSHosts, TLSEmail: r.TLSEmail}, nil
}

func (m *remoteManager) save() error {
	r := m.store.Remote()
	r.Enabled, r.Listen, r.TLSHosts, r.TLSEmail = m.settings.Enabled, m.settings.Listen, m.settings.TLSHosts, m.settings.TLSEmail
	return m.store.SaveRemote(r)
}

func (m *remoteManager) normalize(in app.RemoteSettings) (app.RemoteSettings, error) {
	out := app.RemoteSettings{Enabled: in.Enabled, Listen: strings.TrimSpace(in.Listen), TLSEmail: strings.TrimSpace(in.TLSEmail)}
	if out.Listen == "" {
		out.Listen = defaultRemoteListen
	}
	if err := validateRemoteListen(out.Listen, m.loopback); err != nil {
		return out, err
	}
	var hosts []string
	for _, h := range splitHostList(in.TLSHosts) {
		if net.ParseIP(strings.Trim(h, "[]")) != nil || !validPublicDNSName(h) {
			return out, fmt.Errorf("%q is not a valid public DNS/DDNS name; automatic certificates require a public hostname", h)
		}
		hosts = append(hosts, h)
	}
	if len(hosts) > maxTLSHosts {
		return out, fmt.Errorf("at most %d names or addresses are allowed", maxTLSHosts)
	}
	out.TLSHosts = strings.Join(hosts, ", ")
	if out.TLSEmail != "" {
		address, err := mail.ParseAddress(out.TLSEmail)
		if err != nil || address.Address != out.TLSEmail {
			return out, errors.New("enter a valid contact email address or leave it empty")
		}
	}
	return out, nil
}

func (m *remoteManager) start() (remotePlan, error) {
	plan, err := prepareRemote(m.dataDir, m.store, remoteSettings{
		Listen: m.settings.Listen, CertFile: m.certFile, KeyFile: m.keyFile, ExtraHost: m.settings.TLSHosts, Email: m.settings.TLSEmail,
	})
	if err != nil {
		return remotePlan{}, fmt.Errorf("remote access setup: %w", err)
	}
	raw, err := net.Listen("tcp", m.settings.Listen)
	if err != nil {
		return remotePlan{}, fmt.Errorf("cannot listen on %s: %w", m.settings.Listen, err)
	}
	hosts := m.linkHosts(plan)
	if len(hosts) == 0 {
		_ = raw.Close()
		return remotePlan{}, errors.New("remote access requires at least one usable non-loopback DNS name or IP address in the configured host list or certificate")
	}
	var redirectListener net.Listener
	var redirectServer *http.Server
	redirectAddress, err := remoteHTTPListenAddress(m.settings.Listen)
	if err != nil {
		_ = raw.Close()
		return remotePlan{}, err
	}
	redirectListener, err = net.Listen("tcp", redirectAddress)
	if err != nil {
		_ = raw.Close()
		return remotePlan{}, fmt.Errorf("cannot start HTTP-to-HTTPS redirect on %s (TCP port 80 must be reachable): %w", redirectAddress, err)
	}
	if !plan.Creds.Configured() {
		password, err := plan.Creds.Generate()
		if err != nil {
			_ = redirectListener.Close()
			_ = raw.Close()
			return remotePlan{}, fmt.Errorf("create initial remote access password: %w", err)
		}
		plan.NewPassword = password
	}
	redirectHandler := newHTTPSRedirectHandler(hosts, m.portForRedirect())
	if plan.CertManager != nil {
		redirectHandler = plan.CertManager.HTTPHandler(redirectHandler)
	}
	redirectServer = &http.Server{
		Handler:           redirectHandler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
		IdleTimeout:       30 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	server := &http.Server{
		Handler:           httpapi.NewRemote(m.application, httpapi.RemoteOptions{Auth: plan.Auth, AllowedHosts: hosts}),
		ErrorLog:          log.New(tlsNoiseFilter{}, "", 0),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    64 << 10,
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
	m.server, m.redirect, m.plan, m.running = server, redirectServer, plan, true
	m.closeAll = func() {
		connsMu.Lock()
		defer connsMu.Unlock()
		for c := range conns {
			_ = c.Close()
		}
	}
	if plan.CertManager != nil {
		tlsConfig := plan.CertManager.TLSConfig()
		tlsConfig.MinVersion = tls.VersionTLS12
		server.TLSConfig = tlsConfig
	} else {
		server.TLSConfig = plan.Certificate.ServerConfig()
	}
	_, m.port, _ = net.SplitHostPort(raw.Addr().String())
	m.lastErr = ""
	m.hosts = hosts
	remoteLog.Info("Remote access (HTTPS) is ready", "port", m.port, "certificate", plan.Certificate.Source, "sha256", plan.Certificate.Fingerprint)
	for _, url := range m.links() {
		remoteLog.Info("Open from another device", "url", url)
	}
	if plan.AutoTLS {
		remoteLog.Info("Trusted HTTPS certificates will be obtained and renewed automatically", "hosts", strings.Join(m.hosts, ", "))
	}
	if redirectServer != nil {
		go func() {
			if err := redirectServer.Serve(redirectListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				remoteLog.Error("HTTP-to-HTTPS redirect server", "error", err)
			}
		}()
	}
	go func() {
		if err := server.ServeTLS(raw, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			remoteLog.Error("remote HTTPS server", "error", err)
		}
	}()
	return plan, nil
}

func (m *remoteManager) linkHosts(plan remotePlan) []string {
	hosts := splitHostList(m.settings.TLSHosts)
	if len(hosts) == 0 {
		hosts = splitHostList(strings.Join(plan.Certificate.Hosts, ","))
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
	var urls []string
	for _, host := range m.hosts {
		urls = append(urls, scheme+remoteURLHost(host, m.port))
	}
	return urls
}

func (m *remoteManager) portForRedirect() string {
	_, port, _ := net.SplitHostPort(m.settings.Listen)
	return port
}

func (m *remoteManager) stop() {
	if m.server == nil && m.redirect == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if m.server != nil {
		_ = m.server.Shutdown(ctx)
		_ = m.server.Close()
	}
	if m.redirect != nil {
		_ = m.redirect.Shutdown(ctx)
		_ = m.redirect.Close()
	}
	if m.closeAll != nil {
		m.closeAll()
	}
	if m.plan.Auth != nil {
		m.plan.Auth.RevokeAll()
	}
	m.server, m.redirect, m.closeAll, m.running, m.plan, m.hosts = nil, nil, nil, false, remotePlan{}, nil
	remoteLog.Info("Remote access stopped")
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
		m.stop()
		m.settings = previous
		if previous.Enabled {
			if _, restoreErr := m.start(); restoreErr != nil {
				m.lastErr = fmt.Sprintf("settings could not be saved (%v), and previous remote access could not be restored (%v)", err, restoreErr)
				return m.status(), "", fmt.Errorf("could not save remote settings or restore previous access: %w", err)
			}
		}
		return m.status(), "", fmt.Errorf("could not save remote settings; previous settings were restored: %w", err)
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
