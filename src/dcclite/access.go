package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"

	"github.com/didou07/dcc-lite/src/remoteaccess"
)

type remoteSettings struct {
	Listen    string
	CertFile  string
	KeyFile   string
	ExtraHost string
	Plain     bool
}

type remotePlan struct {
	Auth        *remoteaccess.Auth
	Creds       *remoteaccess.Credentials
	Certificate remoteaccess.Certificate

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

func prepareRemote(dataDir string, store remoteaccess.PasswordStore, settings remoteSettings) (remotePlan, error) {
	if (settings.CertFile == "") != (settings.KeyFile == "") {
		return remotePlan{}, errors.New("--tls-cert and --tls-key must be given together")
	}
	creds, err := remoteaccess.OpenCredentials(store)
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

func manageRemotePassword(store remoteaccess.PasswordStore, set, reset bool, in io.Reader, out io.Writer) (bool, error) {
	if !set && !reset {
		return false, nil
	}
	if set && reset {
		return true, errors.New("use either --set-remote-password or --reset-remote-password, not both")
	}
	creds, err := remoteaccess.OpenCredentials(store)
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

type tlsNoiseFilter struct{}

func (tlsNoiseFilter) Write(p []byte) (int, error) {
	line := strings.TrimSpace(string(p))
	if strings.Contains(line, "TLS handshake error") {
		remoteLog.Debug("remote TLS handshake failed", "detail", line)
		return len(p), nil
	}
	remoteLog.Warn("remote HTTPS server", "detail", line)
	return len(p), nil
}
