package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strconv"
	"strings"

	"github.com/didou07/dcc-lite/src/remoteaccess"
)

// remoteSettings are the command-line options of the optional HTTPS listener.
type remoteSettings struct {
	Listen    string
	CertFile  string
	KeyFile   string
	ExtraHost string // comma-separated names or IPs added to the self-signed certificate
	Plain     bool   // serve plain HTTP instead of HTTPS (not encrypted)
}

// remotePlan is everything needed to start the HTTPS listener.
type remotePlan struct {
	Auth        *remoteaccess.Auth
	Creds       *remoteaccess.Credentials
	Certificate remoteaccess.Certificate
	// NewPassword is set only when a password was generated on this start.
	NewPassword string
}

func validateRemoteListen(address, loopback string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("expected host:port (for example 0.0.0.0:8788): %w", err)
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return errors.New("the remote port must be between 1 and 65535")
	}
	if _, loopbackPort, err := net.SplitHostPort(loopback); err == nil && loopbackPort == port {
		ip := net.ParseIP(strings.Trim(host, "[]"))
		if host == "" || ip == nil || ip.IsUnspecified() || ip.IsLoopback() {
			return errors.New("the remote port must differ from the local --listen port")
		}
	}
	return nil
}

// prepareRemote loads or creates the password and TLS certificate.
func prepareRemote(dataDir string, settings remoteSettings) (remotePlan, error) {
	if (settings.CertFile == "") != (settings.KeyFile == "") {
		return remotePlan{}, errors.New("--tls-cert and --tls-key must be given together")
	}
	creds, err := remoteaccess.OpenCredentials(dataDir)
	if err != nil {
		return remotePlan{}, err
	}
	plan := remotePlan{}
	if !creds.Configured() {
		password, err := creds.Generate()
		if err != nil {
			return remotePlan{}, err
		}
		plan.NewPassword = password
	}
	plan.Creds = creds
	if settings.Plain {
		// No certificate at all: the listener speaks plain HTTP.
		plan.Auth = remoteaccess.NewPlainAuth(creds)
		return plan, nil
	}
	plan.Auth = remoteaccess.NewAuth(creds)

	if settings.CertFile != "" {
		plan.Certificate, err = remoteaccess.LoadProvided(settings.CertFile, settings.KeyFile)
	} else {
		hosts := remoteaccess.DefaultHosts()
		for _, extra := range strings.Split(settings.ExtraHost, ",") {
			hosts = append(hosts, extra)
		}
		if host, _, splitErr := net.SplitHostPort(settings.Listen); splitErr == nil && host != "" {
			if ip := net.ParseIP(strings.Trim(host, "[]")); ip == nil || !ip.IsUnspecified() {
				hosts = append(hosts, host)
			}
		}
		plan.Certificate, err = remoteaccess.LoadOrCreateSelfSigned(dataDir, hosts)
	}
	if err != nil {
		return remotePlan{}, fmt.Errorf("TLS certificate: %w", err)
	}
	return plan, nil
}

// manageRemotePassword implements --set-remote-password and --reset-remote-password.
// It returns true when a management action ran and the program should exit.
func manageRemotePassword(dataDir string, set, reset bool, in io.Reader, out io.Writer) (bool, error) {
	if !set && !reset {
		return false, nil
	}
	if set && reset {
		return true, errors.New("use either --set-remote-password or --reset-remote-password, not both")
	}
	creds, err := remoteaccess.OpenCredentials(dataDir)
	if err != nil {
		return true, err
	}
	if reset {
		password, err := creds.Generate()
		if err != nil {
			return true, err
		}
		fmt.Fprintf(out, "New remote access password (shown once): %s\n", password)
		return true, nil
	}
	fmt.Fprintf(out, "Type the new remote access password (at least %d characters) and press Enter: ", remoteaccess.MinPasswordLength)
	line, err := bufio.NewReader(io.LimitReader(in, 1024)).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return true, err
	}
	password := strings.TrimRight(line, "\r\n")
	if err := creds.Set(password); err != nil {
		return true, err
	}
	fmt.Fprintln(out, "\nRemote access password saved.")
	return true, nil
}

// tlsNoiseFilter drops the "TLS handshake error" lines Go logs whenever a
// browser rejects a self-signed certificate or a scanner connects.
type tlsNoiseFilter struct{}

func (tlsNoiseFilter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if strings.Contains(line, "TLS handshake error") {
		slog.Debug("remote TLS handshake failed", "detail", line)
		return len(p), nil
	}
	slog.Warn("remote HTTPS server", "detail", line)
	return len(p), nil
}
