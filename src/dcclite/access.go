package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/didou07/dcc-lite/src/remoteaccess"
	"golang.org/x/crypto/acme/autocert"
)

type remoteSettings struct {
	Listen    string
	CertFile  string
	KeyFile   string
	ExtraHost string
	Email     string
}

type remotePlan struct {
	Auth        *remoteaccess.Auth
	Creds       *remoteaccess.Credentials
	Certificate remoteaccess.Certificate
	CertManager *autocert.Manager
	AutoTLS     bool

	NewPassword string
}

func validateRemoteListen(address, loopback string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("expected host:port (for example 0.0.0.0:443): %w", err)
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
	plan.Creds = creds
	plan.Auth = remoteaccess.NewAuth(creds)

	if settings.CertFile != "" {
		plan.Certificate, err = remoteaccess.LoadProvided(settings.CertFile, settings.KeyFile)
		if err == nil {
			hosts := splitHostList(settings.ExtraHost)
			if len(hosts) == 0 {
				for _, host := range plan.Certificate.Hosts {
					if !strings.HasPrefix(host, "*.") {
						hosts = append(hosts, host)
					}
				}
			}
			if len(hosts) == 0 {
				if len(plan.Certificate.Hosts) == 0 {
					err = errors.New("the provided certificate has no DNS/IP subject alternative names; use a certificate with SAN entries")
				} else {
					err = errors.New("the provided certificate only has wildcard names; specify a concrete hostname with --tls-host")
				}
			} else {
				for _, host := range hosts {
					if verifyErr := plan.Certificate.VerifyTrustedHost(host); verifyErr != nil {
						err = fmt.Errorf("the provided certificate is not trusted for %q: %w", host, verifyErr)
						break
					}
				}
			}
		}
	} else {
		hosts, hostErr := publicDNSHosts(settings.ExtraHost)
		if hostErr != nil {
			return remotePlan{}, hostErr
		}
		if len(hosts) == 0 {
			return remotePlan{}, errors.New("enter a public DNS/DDNS name to obtain a browser-trusted HTTPS certificate automatically")
		}
		cacheDir := filepath.Join(dataDir, "tls", "acme")
		if err := os.MkdirAll(cacheDir, 0o700); err != nil {
			return remotePlan{}, fmt.Errorf("create certificate cache: %w", err)
		}
		plan.AutoTLS = true
		plan.Certificate.Source = "letsencrypt-auto"
		plan.CertManager = &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			Email:      strings.TrimSpace(settings.Email),
			Cache:      autocert.DirCache(cacheDir),
			HostPolicy: acmeHostPolicy(hosts),
		}
	}
	if err != nil {
		return remotePlan{}, fmt.Errorf("TLS certificate: %w", err)
	}
	return plan, nil
}

func acmeHostPolicy(hosts []string) autocert.HostPolicy {
	allowed := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		allowed[strings.ToLower(strings.TrimSuffix(host, "."))] = struct{}{}
	}
	return func(_ context.Context, requested string) error {
		if host, _, err := net.SplitHostPort(requested); err == nil {
			requested = host
		}
		requested = strings.ToLower(strings.TrimSuffix(strings.Trim(requested, "[]"), "."))
		if _, ok := allowed[requested]; !ok {
			return fmt.Errorf("ACME certificate request for unconfigured host %q was rejected", requested)
		}
		return nil
	}
}

func splitHostList(value string) []string {
	seen := make(map[string]bool)
	var hosts []string
	for _, host := range strings.Split(value, ",") {
		host = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(host), "."))
		if host == "" || seen[host] {
			continue
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	return hosts
}

func publicDNSHosts(value string) ([]string, error) {
	hosts := splitHostList(value)
	if len(hosts) > maxTLSHosts {
		return nil, fmt.Errorf("at most %d DNS names are allowed", maxTLSHosts)
	}
	for _, host := range hosts {
		if net.ParseIP(strings.Trim(host, "[]")) != nil || !validPublicDNSName(host) {
			return nil, fmt.Errorf("%q is not a valid public DNS/DDNS name; automatic certificates require a public hostname, not a private name or IP address", host)
		}
	}
	return hosts, nil
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
