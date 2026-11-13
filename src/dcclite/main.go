package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	"github.com/didou07/dcc-lite/src/logx"
)

var version = "dev"

var (
	appLog    = logx.For("app")
	remoteLog = logx.For("remote")
)

func fatal(code int, msg string, args ...any) {
	appLog.Error(msg, args...)
	logx.Close()
	os.Exit(code)
}

func envOr(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}

func main() {
	listen := flag.String("listen", "127.0.0.1:8787", "Local HTTP listen address (loopback only)")
	dataDir := flag.String("data-dir", "", "Override the application data directory")
	portable := flag.Bool("portable", false, "Store data in a data folder beside the executable")
	configPath := flag.String("config", "", "Settings file (default: dcc-lite.ini beside the program)")
	noBrowser := flag.Bool("no-browser", false, "Do not open the web interface automatically")
	showVersion := flag.Bool("version", false, "Print version and exit")
	remote := remoteSettings{}
	flag.StringVar(&remote.Listen, "remote-listen", "", "Also serve the interface over HTTPS with a password on this address, for example 0.0.0.0:443 (off by default)")
	flag.StringVar(&remote.CertFile, "tls-cert", "", "Trusted PEM certificate for remote HTTPS (automatic Let's Encrypt certificate is used when omitted)")
	flag.StringVar(&remote.KeyFile, "tls-key", "", "PEM private key for --tls-cert")
	flag.StringVar(&remote.ExtraHost, "tls-host", "", "Public DNS/DDNS names for automatic trusted HTTPS certificates, comma separated")
	flag.StringVar(&remote.Email, "tls-email", "", "Optional contact email for automatic HTTPS certificate registration")
	logLevel := flag.String("log-level", envOr("DCCLITE_LOG_LEVEL", "info"), "Console log level: trace, debug, info, warn or error")
	logFileLevel := flag.String("log-file-level", envOr("DCCLITE_LOG_FILE_LEVEL", "debug"), "Log file level: trace, debug, info, warn or error")
	logDir := flag.String("log-dir", "", "Folder for dcclite.log and faults.json (default: logs inside the data directory; \"off\" disables the file)")
	logFormat := flag.String("log-format", "text", "Log file format: text or json (JSON lines)")
	debug := flag.Bool("debug", false, "Shortcut for --log-level debug")
	setPassword := flag.Bool("set-remote-password", false, "Read a new remote access password from standard input, save it and exit")
	resetPassword := flag.Bool("reset-remote-password", false, "Generate a new random remote access password, print it and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("DCC Lite %s (%s/%s)\n", version, runtime.GOOS, runtime.GOARCH)
		return
	}

	if *debug && *logLevel == "info" {
		*logLevel = "debug"
	}
	consoleLevel, err := logx.ParseLevel(*logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, "--log-level:", err)
		os.Exit(2)
	}
	fileLevel, err := logx.ParseLevel(*logFileLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, "--log-file-level:", err)
		os.Exit(2)
	}
	if *logFormat != "text" && *logFormat != "json" {
		fmt.Fprintln(os.Stderr, "--log-format: use text or json")
		os.Exit(2)
	}

	root, err := resolveDataDir(*dataDir, *portable)
	if err != nil {
		fmt.Fprintln(os.Stderr, "resolve data directory:", err)
		os.Exit(1)
	}
	if err := os.MkdirAll(root, 0o700); err != nil {
		fmt.Fprintln(os.Stderr, "create data directory:", err)
		os.Exit(1)
	}

	dir := *logDir
	if dir == "" {
		dir = filepath.Join(root, "logs")
	}
	if strings.EqualFold(dir, "off") {
		dir = ""
	}
	hub, logErr := logx.Init(logx.Options{
		ConsoleLevel: consoleLevel, FileLevel: fileLevel, Dir: dir,
		JSON: *logFormat == "json",
	})
	defer func() {
		if r := recover(); r != nil {
			logx.Panic(appLog, r, "DCC Lite")
			logx.Close()
			os.Exit(2)
		}
	}()
	defer hub.Close()
	if logErr != nil {
		appLog.Warn("log file disabled", "dir", dir, "error", logErr)
	}
	hub.SessionStart(version)

	if err := validateListenAddress(*listen); err != nil {
		fatal(2, "invalid listen address", "error", err)
	}
	if remote.Listen != "" {
		if err := validateRemoteListen(remote.Listen, *listen); err != nil {
			fatal(2, "invalid --remote-listen address", "error", err)
		}
	}

	iniPath := resolveConfigPath(*configPath, root)
	store, err := config.NewProfileStore(root, iniPath)
	if err != nil {
		fatal(1, "initialize profile store", "error", err)
	}

	if *setPassword || *resetPassword {
		if _, err := manageRemotePassword(store, *setPassword, *resetPassword, os.Stdin, os.Stdout); err != nil {
			fatal(2, "remote password", "error", err)
		}
		return
	}

	listener, err := net.Listen("tcp", *listen)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) && isExistingDCCLite(*listen) {
			existingURL := "http://" + *listen
			appLog.Info("DCC Lite is already running", "url", existingURL)
			if !*noBrowser {
				openBrowser(existingURL)
			}
			return
		}
		fatal(1, "listen", "address", *listen, "error", err)
	}
	defer listener.Close()

	application := app.New(version, store, root)
	defer application.Close()
	server := &http.Server{
		Addr:              *listen,
		Handler:           httpapi.New(application),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}

	url := "http://" + listener.Addr().String()
	appLog.Info("DCC Lite is ready", "url", url, "version", version)
	appLog.Debug("paths", "data_dir", root, "settings", store.Path(), "log_dir", hub.Dir())

	manager := newRemoteManager(application, store, root, *listen)
	application.RemoteAccess = manager
	if err := manager.Init(remote); err != nil {
		fatal(1, "remote access setup", "error", err)
	}
	defer manager.Stop()
	if !*noBrowser {
		openBrowser(url)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logx.Go(appLog, "shutdown watcher", func() {
		<-ctx.Done()
		appLog.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		manager.Stop()
		if err := server.Shutdown(shutdownCtx); err != nil {
			appLog.Error("graceful shutdown", "error", err)
		}
	})

	if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
		fatal(1, "HTTP server", "error", err)
	}
}

func displayHosts(hosts []string) []string {
	var out []string
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsLinkLocalUnicast()) {
			continue
		}
		if strings.EqualFold(host, "localhost") {
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
	return dir, nil
}

func resolveConfigPath(override, dataDir string) string {
	if override != "" {
		return override
	}
	if exe, err := os.Executable(); err == nil {
		if real, err := filepath.EvalSymlinks(exe); err == nil {
			exe = real
		}
		dir := filepath.Dir(exe)
		if !strings.HasPrefix(dir, os.TempDir()) && dirWritable(dir) {
			return filepath.Join(dir, config.IniFileName)
		}
	}
	return filepath.Join(dataDir, config.IniFileName)
}

func dirWritable(dir string) bool {
	f, err := os.CreateTemp(dir, ".dcclite-write-test-*")
	if err != nil {
		return false
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true
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
		appLog.Warn("could not open browser automatically", "error", err, "url", url)
	}
}
