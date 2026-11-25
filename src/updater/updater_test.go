//go:build !windows

package updater

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

const oldBinary = "old-binary"

type fakeRelease struct {
	tag        string
	prerelease bool
	draft      bool
	binary     string // contents of DCCLite inside the zip
	extraFiles map[string]string
	badSum     bool
	noSums     bool
	noPackage  bool
}

type testEnv struct {
	u       *Updater
	exe     string
	dataDir string
	srv     *httptest.Server
}

func scriptReporting(version string) string {
	return "#!/bin/sh\necho 'DCC Lite " + version + "'\n"
}

func newEnv(t *testing.T, current string, rel fakeRelease) *testEnv {
	t.Helper()
	if rel.binary == "" {
		rel.binary = scriptReporting(strings.TrimPrefix(rel.tag, "v"))
	}
	version := strings.TrimPrefix(rel.tag, "v")
	pkgName := fmt.Sprintf("DCC-Lite-%s-%s-%s.zip", version, runtime.GOOS, runtime.GOARCH)

	var zbuf bytes.Buffer
	zw := zip.NewWriter(&zbuf)
	w, _ := zw.Create("DCCLite")
	io.WriteString(w, rel.binary)
	for name, body := range rel.extraFiles {
		w, _ := zw.Create(name)
		io.WriteString(w, body)
	}
	zw.Close()
	sum := sha256.Sum256(zbuf.Bytes())
	sumHex := hex.EncodeToString(sum[:])
	if rel.badSum {
		sumHex = strings.Repeat("0", 64)
	}

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/repos/owner/repo/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		assets := []asset{}
		if !rel.noPackage {
			assets = append(assets, asset{Name: pkgName, Size: int64(zbuf.Len()), URL: srv.URL + "/dl/" + pkgName})
		}
		if !rel.noSums {
			assets = append(assets, asset{Name: "SHA256SUMS.txt", URL: srv.URL + "/dl/SHA256SUMS.txt"})
		}
		json.NewEncoder(w).Encode(release{Tag: rel.tag, HTML: "https://example/rel", Body: "notes", Prerelease: rel.prerelease, Draft: rel.draft, Assets: assets})
	})
	mux.HandleFunc("/dl/"+pkgName, func(w http.ResponseWriter, r *http.Request) { w.Write(zbuf.Bytes()) })
	mux.HandleFunc("/dl/SHA256SUMS.txt", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  %s\n%s  other-file.zip\n", sumHex, pkgName, strings.Repeat("1", 64))
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	exe := filepath.Join(dir, "DCCLite")
	if err := os.WriteFile(exe, []byte(oldBinary), 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(dir, "data")
	u := New(Options{
		Repo: "owner/repo", Version: current, Variant: "test", DataDir: dataDir,
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), APIBase: srv.URL, ExecPath: exe,
		Client: srv.Client(), trustAll: true,
	})
	return &testEnv{u: u, exe: exe, dataDir: dataDir, srv: srv}
}

func (e *testEnv) exeContents(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(e.exe)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func TestCheckFindsNewerRelease(t *testing.T) {
	e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0"})
	st, err := e.u.Check(ctx(t))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Available || st.Latest != "1.2.0" || st.Current != "1.1.9" || st.Notes != "notes" || !e.u.Status().Supported {
		t.Fatalf("status = %+v", st)
	}
}

func TestCheckSameOrOlderVersionIsNotAnUpdate(t *testing.T) {
	for _, tag := range []string{"v1.1.9", "v1.1.8"} {
		e := newEnv(t, "1.1.9", fakeRelease{tag: tag})
		st, err := e.u.Check(ctx(t))
		if err != nil || st.Available {
			t.Fatalf("%s: available=%v err=%v", tag, st.Available, err)
		}
	}
}

func TestCheckRejectsUnstableOrIncompleteReleases(t *testing.T) {
	for name, rel := range map[string]fakeRelease{
		"prerelease":   {tag: "v2.0.0", prerelease: true},
		"draft":        {tag: "v2.0.0", draft: true},
		"junk tag":     {tag: "nightly"},
		"no package":   {tag: "v2.0.0", noPackage: true},
		"no checksums": {tag: "v2.0.0", noSums: true},
	} {
		e := newEnv(t, "1.1.9", rel)
		st, err := e.u.Check(ctx(t))
		if err == nil || st.Available {
			t.Errorf("%s: err=%v available=%v", name, err, st.Available)
		}
		if err := e.u.Install(ctx(t)); err == nil {
			t.Errorf("%s: install went ahead", name)
		}
		if e.exeContents(t) != oldBinary {
			t.Errorf("%s: program was modified", name)
		}
	}
}

func TestInstallReplacesProgramAndRequestsRestart(t *testing.T) {
	e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0"})
	if _, err := e.u.Check(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if err := e.u.Install(ctx(t)); err != nil {
		t.Fatal(err)
	}
	if got := e.exeContents(t); got != scriptReporting("1.2.0") {
		t.Fatalf("program = %q", got)
	}
	if info, _ := os.Stat(e.exe); info.Mode()&0o111 == 0 {
		t.Error("new program is not executable")
	}
	select {
	case <-e.u.Restart():
	default:
		t.Fatal("restart was not requested")
	}
	if st := e.u.Status(); st.Busy != "restarting" || st.Available {
		t.Fatalf("status = %+v", st)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(e.exe), ".DCCLite.new")); len(leftovers) != 0 {
		t.Error("temporary file left next to the program")
	}
}

func TestInstallRefusesBadChecksumAndLeavesProgramAlone(t *testing.T) {
	e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0", badSum: true})
	e.u.Check(ctx(t))
	err := e.u.Install(ctx(t))
	if err == nil || !strings.Contains(err.Error(), "SHA-256") {
		t.Fatalf("err = %v", err)
	}
	if e.exeContents(t) != oldBinary {
		t.Fatal("program replaced despite a bad checksum")
	}
	select {
	case <-e.u.Restart():
		t.Fatal("restart requested after a failed install")
	default:
	}
	if st := e.u.Status(); st.Busy != "" || st.Error == "" {
		t.Fatalf("status after failure = %+v", st)
	}
	if left, _ := filepath.Glob(filepath.Join(e.dataDir, "updates", "*")); len(left) != 0 {
		t.Errorf("downloaded package left behind: %v", left)
	}
}

func TestInstallRefusesProgramThatReportsWrongVersionOrFailsToStart(t *testing.T) {
	cases := map[string]string{
		"wrong version": scriptReporting("9.9.9"),
		"crashes":       "#!/bin/sh\nexit 3\n",
		"not a program": "this is not executable",
	}
	for name, bin := range cases {
		e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0", binary: bin})
		e.u.Check(ctx(t))
		if err := e.u.Install(ctx(t)); err == nil {
			t.Errorf("%s: install succeeded", name)
		}
		if e.exeContents(t) != oldBinary {
			t.Errorf("%s: program replaced", name)
		}
	}
}

func TestInstallRefusesUnexpectedPackageLayout(t *testing.T) {
	e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0", extraFiles: map[string]string{"../../evil.sh": "x"}})
	e.u.Check(ctx(t))
	if err := e.u.Install(ctx(t)); err == nil || !strings.Contains(err.Error(), "layout") {
		t.Fatalf("err = %v", err)
	}
	if e.exeContents(t) != oldBinary {
		t.Fatal("program replaced")
	}
	e = newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0", extraFiles: map[string]string{"README.txt": "hello"}})
	e.u.Check(ctx(t))
	if err := e.u.Install(ctx(t)); err != nil {
		t.Fatalf("a README.txt is allowed: %v", err)
	}
}

func TestInstallOnlyTrustsProjectGitHubReleases(t *testing.T) {
	e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0"})
	e.u.Check(ctx(t))
	e.u.opt.trustAll = false // the test server is not github.com
	if err := e.u.Install(ctx(t)); err == nil || !strings.Contains(err.Error(), "GitHub") {
		t.Fatalf("err = %v", err)
	}
	for url, want := range map[string]bool{
		"https://github.com/owner/repo/releases/download/v1/a.zip":  true,
		"http://github.com/owner/repo/releases/download/v1/a.zip":   false,
		"https://github.com/other/repo/releases/download/v1/a.zip":  false,
		"https://evil.com/owner/repo/releases/download/v1/a.zip":    false,
		"https://github.com.evil.com/owner/repo/releases/download/": false,
		"https://github.com/owner/repo/archive/main.zip":            false,
	} {
		if got := e.u.trusted(url); got != want {
			t.Errorf("trusted(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestOnlyOneOperationAtATime(t *testing.T) {
	e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0"})
	if err := e.u.begin("checking"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.u.Check(ctx(t)); err != errBusy {
		t.Fatalf("err = %v, want errBusy", err)
	}
	if err := e.u.Install(ctx(t)); err != errBusy {
		t.Fatalf("err = %v, want errBusy", err)
	}
	e.u.end(nil)
}

func TestDevelopmentBuildsNeverUpdate(t *testing.T) {
	e := newEnv(t, "dev", fakeRelease{tag: "v1.2.0"})
	if st := e.u.Status(); st.Supported {
		t.Fatal("a dev build claims to support updates")
	}
	if _, err := e.u.Check(ctx(t)); err == nil {
		t.Fatal("dev build checked for updates")
	}
}

func TestNoticePreferencePersists(t *testing.T) {
	e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0"})
	if !e.u.Status().Notify {
		t.Fatal("default changed: notices are expected to start enabled")
	}
	if err := e.u.SetNotify(false); err != nil {
		t.Fatal(err)
	}
	again := New(Options{Version: "1.1.9", DataDir: e.dataDir, ExecPath: e.exe})
	if again.Status().Notify {
		t.Fatal("the choice was not remembered")
	}
	os.WriteFile(filepath.Join(e.dataDir, "update.json"), []byte("{not json"), 0o600)
	if !New(Options{Version: "1.1.9", DataDir: e.dataDir, ExecPath: e.exe}).Status().Notify {
		t.Fatal("a damaged preferences file should fall back to the default")
	}
}

func TestOldPreferenceFileIsStillRead(t *testing.T) {
	e := newEnv(t, "1.1.9", fakeRelease{tag: "v1.2.0"})
	if err := os.MkdirAll(e.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.dataDir, "update.json"), []byte(`{"auto":false}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if New(Options{Version: "1.1.9", DataDir: e.dataDir, ExecPath: e.exe}).Status().Notify {
		t.Fatal("an old opt-out was lost")
	}
}

func TestFindChecksum(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	text := sum + "  a.zip\r\n" + strings.ToUpper(sum[:60]) + "FFFF *b.zip\n" + sum + " ./c.zip\nshort  d.zip\n" + strings.Repeat("zz", 32) + "  e.zip\n"
	for name, want := range map[string]bool{"a.zip": true, "b.zip": true, "c.zip": true, "d.zip": false, "e.zip": false, "missing.zip": false} {
		_, err := findChecksum(text, name)
		if (err == nil) != want {
			t.Errorf("%s: err=%v, want ok=%v", name, err, want)
		}
	}
}
