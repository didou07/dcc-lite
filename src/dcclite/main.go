package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/httpapi"
)

var version = "dev"

func main() {
	listen := flag.String("listen", "127.0.0.1:8787", "Local HTTP listen address (loopback only)")
	dataDir := flag.String("data-dir", "", "Override the application data directory")
	portable := flag.Bool("portable", false, "Store data in a data folder beside the executable")
	noBrowser := flag.Bool("no-browser", false, "Do not open the web interface automatically")
	showVersion := flag.Bool("version", false, "Print version and exit")
	remote := remoteSettings{}
	flag.StringVar(&remote.Listen, "remote-listen", "", "Also serve the interface over HTTPS with a password on this address, for example 0.0.0.0:8788 (off by default)")
	flag.StringVar(&remote.CertFile, "tls-cert", "", "PEM certificate for the remote HTTPS listener (default: generate a self-signed one)")
	flag.StringVar(&remote.KeyFile, "tls-key", "", "PEM private key for --tls-cert")
	flag.StringVar(&remote.ExtraHost, "tls-host", "", "Extra host names or IPs to put in the self-signed certificate, comma separated")
	flag.BoolVar(&remote.Plain, "remote-http", false, "Serve --remote-listen as plain HTTP without encryption (password and data are readable on the network; use only on a trusted network)")
	setPassword := flag.Bool("set-remote-password", false, "Read a new remote access password from standard input, save it and exit")
	resetPassword := flag.Bool("reset-remote-password", false, "Generate a new random remote access password, print it and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("DCC Lite %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return
	}

	if err := validateListenAddress(*listen); err != nil {
		slog.Error("invalid listen address", "error", err)
		os.Exit(2)
	}
	if remote.Listen != "" {
		if err := validateRemoteListen(remote.Listen, *listen); err != nil {
			slog.Error("invalid --remote-listen address", "error", err)
			os.Exit(2)
		}
	}
	if *setPassword || *resetPassword {
		root, err := resolveDataDir(*dataDir, *portable)
		if err != nil {
			slog.Error("resolve data directory", "error", err)
			os.Exit(1)
		}
		if _, err := manageRemotePassword(root, *setPassword, *resetPassword, os.Stdin, os.Stdout); err != nil {
			slog.Error("remote password", "error", err)
			os.Exit(2)
		}
		return
	}

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) && isExistingDCCLite(*listen) {
			existingURL := "http://" + *listen
			slog.Info("DCC Lite is already running", "url", existingURL)
			if !*noBrowser {
				openBrowser(existingURL)
			}
			return
		}
		slog.Error("listen", "address", *listen, "error", err)
		os.Exit(1)
	}
	defer listener.Close()

	root, err := resolveDataDir(*dataDir, *portable)
	if err != nil {
		slog.Error("resolve data directory", "error", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		slog.Error("create data directory", "error", err)
		os.Exit(1)
	}

	store, err := config.NewProfileStore(root)
	if err != nil {
		slog.Error("initialize profile store", "error", err)
		os.Exit(1)
	}

	if err := removeLegacyVault(root); err != nil {
		slog.Warn("could not remove obsolete credential vault file", "error", err)
	}

	application := app.New(version, store, root)
	defer application.Terminals.CloseAll()
	defer application.Files.CloseAll()
	server := &http.Server{
		Addr:              *listen,
		Handler:           httpapi.New(application),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	url := "http://" + listener.Addr().String()
	slog.Info("DCC Lite is ready", "url", url, "data_dir", root, "version", version)

	manager := newRemoteManager(application, root, *listen)
	application.RemoteAccess = manager
	if err := manager.Init(remote); err != nil {
		slog.Error("remote access setup", "error", err)
		os.Exit(1)
	}
	defer manager.Stop()
	if !*noBrowser {
		openBrowser(url)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		manager.Stop()
		if err := server.Shutdown(shutdownCtx); err != nil {
			slog.Error("graceful shutdown", "error", err)
		}
	}()

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		slog.Error("HTTP server", "error", err)
		os.Exit(1)
	}
}

func displayHosts(hosts []string) []string {
	var out []string
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
			continue
		}
		if host == "localhost" {
			continue
		}
		out = append(out, host)
	}
	return out
}

func isExistingDCCLite(address string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Millisecond)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+address+"/api/v1/health", nil)
	if err != nil {
		return false
	}
	transport := &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 700 * time.Millisecond}).DialContext}
	client := &http.Client{Transport: transport, Timeout: 900 * time.Millisecond}
	defer transport.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return false
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return false
	}
	var health struct {
		OK      bool   `json:"ok"`
		Version string `json:"version"`
		API     string `json:"api"`
	}
	if err := json.NewDecoder(io.LimitReader(response.Body, 4096)).Decode(&health); err != nil {
		return false
	}
	return health.OK && health.API == "v1" && health.Version != ""
}

func validateListenAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("expected host:port: %w", err)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("DCC Lite currently accepts loopback addresses only; use 127.0.0.1 or [::1]")
	}
	return nil
}

func resolveDataDir(override string, portable bool) (string, error) {
	if override != "" {
		return override, nil
	}
	if portable {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.Join(filepath.Dir(exe), "data"), nil
	}
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "DCCLite")

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		for _, old := range []string{"BoxDeck", "DCCNext"} {
			legacy := filepath.Join(base, old)
			if _, err := os.Stat(legacy); err != nil {
				continue
			}
			if err := os.Rename(legacy, dir); err != nil {
				return legacy, nil
			}
			break
		}
	}
	return dir, nil
}

func openBrowser(url string) {

	var cmd string
	var args []string
	switch runtime.GOOS {
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		cmd, args = "open", []string{url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	if err := startDetached(cmd, args...); err != nil {
		slog.Warn("could not open browser automatically", "error", err, "url", url)
	}
}

func removeLegacyVault(dataDir string) error {
	err := os.Remove(filepath.Join(dataDir, "vault.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
