// Package updater installs newer DCC Lite releases from GitHub.
package updater

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

const (
	DefaultRepo   = "didou07/dcc-lite"
	checkEvery    = 6 * time.Hour
	retryBusyIn   = 10 * time.Minute
	firstCheckIn  = 30 * time.Second
	maxPackage    = 256 << 20
	maxBinary     = 512 << 20
	maxAPIPayload = 4 << 20
)

type Options struct {
	Repo    string // owner/name, default DefaultRepo
	Version string // running version, "dev" disables updating
	Variant string
	DataDir string // stores update.json and downloads
	Log     *slog.Logger
	Idle    func() bool // auto-install waits until this returns true (nil = always idle)

	// Overridable for tests.
	APIBase  string
	ExecPath string
	Client   *http.Client
	trustAll bool
}

type Status struct {
	Current    string    `json:"current"`
	Latest     string    `json:"latest,omitempty"`
	Available  bool      `json:"available"`
	Auto       bool      `json:"auto"`
	Supported  bool      `json:"supported"`
	Reason     string    `json:"reason,omitempty"`
	Busy       string    `json:"busy,omitempty"` // "checking", "installing" or "restarting"
	CheckedAt  time.Time `json:"checked_at,omitempty"`
	ReleaseURL string    `json:"release_url,omitempty"`
	Notes      string    `json:"notes,omitempty"`
	Error      string    `json:"error,omitempty"`
}

type Updater struct {
	opt     Options
	log     *slog.Logger
	client  *http.Client
	exe     string
	restart chan struct{}

	mu       sync.Mutex
	st       Status
	asset    asset
	sums     asset
	failed   string // version whose auto-install failed this run
	restarts sync.Once
}

type asset struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
	URL  string `json:"browser_download_url"`
}

type release struct {
	Tag        string  `json:"tag_name"`
	HTML       string  `json:"html_url"`
	Body       string  `json:"body"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []asset `json:"assets"`
}

func New(opt Options) *Updater {
	if opt.Repo == "" {
		opt.Repo = DefaultRepo
	}
	if opt.APIBase == "" {
		opt.APIBase = "https://api.github.com"
	}
	u := &Updater{opt: opt, log: opt.Log, restart: make(chan struct{})}
	if u.log == nil {
		u.log = slog.Default()
	}
	u.client = opt.Client
	if u.client == nil {
		u.client = &http.Client{Timeout: 15 * time.Minute}
	}
	u.exe = opt.ExecPath
	if u.exe == "" {
		if p, err := os.Executable(); err == nil {
			u.exe = p
		}
	}
	if real, err := filepath.EvalSymlinks(u.exe); err == nil {
		u.exe = real
	}
	u.st.Current = opt.Version
	u.st.Auto = u.loadAuto()
	u.st.Supported, u.st.Reason = u.supported()
	return u
}

// ExecPath is the program path captured at startup (os.Executable reports a
// "(deleted)" path on Linux once the file has been replaced).
func (u *Updater) ExecPath() string { return u.exe }

// Restart is closed once a new executable is in place and the program should relaunch.
func (u *Updater) Restart() <-chan struct{} { return u.restart }

func (u *Updater) supported() (bool, string) {
	if _, ok := parseVersion(u.opt.Version); !ok {
		return false, "Development build; updates are disabled"
	}
	if u.exe == "" {
		return false, "Cannot locate the running program"
	}
	if !u.opt.trustAll && strings.HasPrefix(u.exe, os.TempDir()+string(os.PathSeparator)) {
		return false, "Running from a temporary folder; updates are disabled"
	}
	f, err := os.CreateTemp(filepath.Dir(u.exe), ".dcclite-write-test-*")
	if err != nil {
		return false, "The program folder is not writable; install updates manually"
	}
	name := f.Name()
	f.Close()
	os.Remove(name)
	return true, ""
}

func (u *Updater) Status() Status {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.st
}

// DisableAuto forces automatic installation off for this run (e.g. --no-auto-update).
func (u *Updater) DisableAuto() {
	u.mu.Lock()
	u.st.Auto = false
	u.mu.Unlock()
}

type prefs struct {
	Auto *bool `json:"auto,omitempty"`
}

func (u *Updater) prefsPath() string { return filepath.Join(u.opt.DataDir, "update.json") }

func (u *Updater) loadAuto() bool {
	data, err := os.ReadFile(u.prefsPath())
	if err != nil {
		return true // automatic updates are on by default
	}
	var p prefs
	if json.Unmarshal(data, &p) != nil || p.Auto == nil {
		return true
	}
	return *p.Auto
}

func (u *Updater) SetAuto(auto bool) error {
	data, _ := json.Marshal(prefs{Auto: &auto})
	if err := os.MkdirAll(u.opt.DataDir, 0o700); err != nil {
		return err
	}
	tmp := u.prefsPath() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, u.prefsPath()); err != nil {
		return err
	}
	u.mu.Lock()
	u.st.Auto = auto
	u.mu.Unlock()
	return nil
}

func (u *Updater) assetName() string {
	suffix := ""
	switch u.opt.Variant {
	case "win7":
		suffix = "-win7"
	case "gui":
		suffix = "-gui"
	}
	return fmt.Sprintf("DCC-Lite-%s-%s-%s%s.zip", "%s", runtime.GOOS, runtime.GOARCH, suffix)
}

func (u *Updater) trusted(raw string) bool {
	if u.opt.trustAll {
		return true
	}
	p, err := url.Parse(raw)
	return err == nil && p.Scheme == "https" && p.Host == "github.com" &&
		strings.HasPrefix(p.Path, "/"+u.opt.Repo+"/releases/download/")
}

var errBusy = errors.New("another update operation is running")

func (u *Updater) begin(kind string) error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.st.Busy != "" {
		return errBusy
	}
	u.st.Busy = kind
	return nil
}

func (u *Updater) end(err error) {
	u.mu.Lock()
	u.st.Busy = ""
	if err != nil {
		u.st.Error = err.Error()
	} else {
		u.st.Error = ""
	}
	u.mu.Unlock()
}

// Check asks GitHub for the latest stable release.
func (u *Updater) Check(ctx context.Context) (Status, error) {
	if err := u.begin("checking"); err != nil {
		return u.Status(), err
	}
	err := u.check(ctx)
	u.end(err)
	return u.Status(), err
}

func (u *Updater) check(ctx context.Context) error {
	u.mu.Lock()
	supported := u.st.Supported
	u.mu.Unlock()
	if !supported {
		return errors.New(u.Status().Reason)
	}
	endpoint := fmt.Sprintf("%s/repos/%s/releases/latest", u.opt.APIBase, u.opt.Repo)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "DCCLite/"+u.opt.Version)
	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("no published release found")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("check for updates: GitHub answered %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxAPIPayload)).Decode(&rel); err != nil {
		return fmt.Errorf("check for updates: %w", err)
	}
	latest := strings.TrimPrefix(rel.Tag, "v")
	if _, ok := parseVersion(latest); !ok || rel.Draft || rel.Prerelease {
		return fmt.Errorf("latest release %q is not a stable version", rel.Tag)
	}
	st := Status{CheckedAt: time.Now().UTC(), Latest: latest, ReleaseURL: rel.HTML, Notes: truncate(rel.Body, 4000)}
	var pkg, sums asset
	if newer(latest, u.opt.Version) {
		want := fmt.Sprintf(u.assetName(), latest)
		for _, a := range rel.Assets {
			switch a.Name {
			case want:
				pkg = a
			case "SHA256SUMS.txt":
				sums = a
			}
		}
		if pkg.Name == "" || sums.Name == "" {
			u.mu.Lock()
			u.st.CheckedAt, u.st.Latest, u.st.Available = st.CheckedAt, latest, false
			u.mu.Unlock()
			return fmt.Errorf("release v%s has no %s package or checksum file", latest, want)
		}
		st.Available = true
	}
	u.mu.Lock()
	u.st.CheckedAt, u.st.Latest, u.st.Available = st.CheckedAt, st.Latest, st.Available
	u.st.ReleaseURL, u.st.Notes = st.ReleaseURL, st.Notes
	u.asset, u.sums = pkg, sums
	u.mu.Unlock()
	return nil
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Install downloads, verifies and swaps in the release found by the last Check,
// then signals Restart.
func (u *Updater) Install(ctx context.Context) error {
	if err := u.begin("installing"); err != nil {
		return err
	}
	err := u.install(ctx)
	if err != nil {
		u.end(err)
		return err
	}
	u.mu.Lock()
	u.st.Busy, u.st.Error, u.st.Available = "restarting", "", false
	u.mu.Unlock()
	u.restarts.Do(func() { close(u.restart) })
	return nil
}

func (u *Updater) install(ctx context.Context) error {
	u.mu.Lock()
	pkg, sums, latest := u.asset, u.sums, u.st.Latest
	available, supported := u.st.Available, u.st.Supported
	u.mu.Unlock()
	if !supported {
		return errors.New(u.Status().Reason)
	}
	if !available || pkg.Name == "" {
		return errors.New("no update is available; check for updates first")
	}
	if !u.trusted(pkg.URL) || !u.trusted(sums.URL) {
		return errors.New("release assets are not hosted on the project's GitHub releases")
	}
	want, err := u.fetchChecksum(ctx, sums.URL, pkg.Name)
	if err != nil {
		return err
	}
	dir := filepath.Join(u.opt.DataDir, "updates")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	zipPath := filepath.Join(dir, pkg.Name)
	defer os.Remove(zipPath)
	if err := u.download(ctx, pkg.URL, zipPath, want); err != nil {
		return err
	}
	newExe := filepath.Join(filepath.Dir(u.exe), "."+filepath.Base(u.exe)+".new")
	defer os.Remove(newExe)
	if err := extractBinary(zipPath, newExe, u.opt.Variant); err != nil {
		return err
	}
	if err := verifyBinary(ctx, newExe, latest); err != nil {
		return err
	}
	if err := swap(u.exe, newExe); err != nil {
		return fmt.Errorf("replace program: %w", err)
	}
	u.log.Info("update installed", "from", u.opt.Version, "to", latest, "next", "restart")
	return nil
}

func (u *Updater) fetchChecksum(ctx context.Context, rawURL, name string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "DCCLite/"+u.opt.Version)
	resp, err := u.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("download checksums: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download checksums: %s", resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", err
	}
	return findChecksum(string(data), name)
}

func findChecksum(text, name string) (string, error) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(strings.TrimPrefix(fields[1], "*"), "./") == name {
			sum := strings.ToLower(fields[0])
			if len(sum) == 64 {
				if _, err := hex.DecodeString(sum); err == nil {
					return sum, nil
				}
			}
		}
	}
	return "", fmt.Errorf("no checksum listed for %s", name)
}

func (u *Updater) download(ctx context.Context, rawURL, dest, wantSum string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "DCCLite/"+u.opt.Version)
	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download update: %s", resp.Status)
	}
	f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(resp.Body, maxPackage+1))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("download update: %w", err)
	}
	if n > maxPackage {
		return errors.New("update package is unexpectedly large")
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != wantSum {
		return errors.New("update package failed its SHA-256 check; it was not installed")
	}
	return nil
}

// binaryName is the executable inside a release package: the Windows app has its own file name so both
// programs can sit in the same folder.
func binaryName(variant string) string {
	name := "DCCLite"
	if variant == "gui" {
		name = "DCCLite-GUI"
	}
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return name
}

func extractBinary(zipPath, dest, variant string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open update package: %w", err)
	}
	defer zr.Close()
	exeName := binaryName(variant)
	if len(zr.File) != 1 || zr.File[0].Name != exeName || zr.File[0].UncompressedSize64 > maxBinary {
		return errors.New("update package has an unexpected layout")
	}
	rc, err := zr.File[0].Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	n, err := io.Copy(out, io.LimitReader(rc, maxBinary+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err == nil && n > maxBinary {
		err = errors.New("update executable is unexpectedly large")
	}
	if err != nil {
		os.Remove(dest)
		return fmt.Errorf("extract update: %w", err)
	}
	return os.Chmod(dest, 0o755)
}

// verifyBinary runs the new executable's --version so a broken or foreign build
// is never swapped in.
func verifyBinary(ctx context.Context, path, version string) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return fmt.Errorf("new program does not start: %w", err)
	}
	if !strings.Contains(string(out), "DCC Lite "+version) {
		return fmt.Errorf("new program reports an unexpected version: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// CleanupOld removes the previous executable left behind by a Windows update.
func (u *Updater) CleanupOld() {
	if u.exe != "" {
		_ = os.Remove(u.exe + ".old")
	}
	_ = os.RemoveAll(filepath.Join(u.opt.DataDir, "updates"))
}

// Run checks periodically and installs automatically when enabled and idle.
func (u *Updater) Run(ctx context.Context) {
	if ok, _ := u.supported(); !ok {
		return
	}
	wait := firstCheckIn
	announced := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = checkEvery
		if _, err := u.Check(ctx); err != nil {
			u.log.Debug("update check failed", "error", err)
			continue
		}
		st := u.Status()
		if st.Available && st.Latest != announced {
			announced = st.Latest
			u.log.Info("new version available", "current", st.Current, "latest", st.Latest, "automatic", st.Auto)
		}
		if !st.Available || !st.Auto || st.Latest == u.failedVersion() {
			continue
		}
		if u.opt.Idle != nil && !u.opt.Idle() {
			u.log.Info("update postponed: file transfers are running", "version", st.Latest, "retry_in", retryBusyIn)
			wait = retryBusyIn
			continue
		}
		u.log.Info("installing update", "version", st.Latest)
		if err := u.Install(ctx); err != nil {
			u.log.Warn("automatic update failed; the current version keeps running", "version", st.Latest, "error", err)
			u.mu.Lock()
			u.failed = st.Latest
			u.mu.Unlock()
			continue
		}
		return
	}
}

func (u *Updater) failedVersion() string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.failed
}
