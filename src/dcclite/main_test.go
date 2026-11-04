package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateListenAddress(t *testing.T) {
	valid := []string{"127.0.0.1:8787", "[::1]:8787"}
	for _, address := range valid {
		t.Run(address, func(t *testing.T) {
			if err := validateListenAddress(address); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
	invalid := []string{"0.0.0.0:8787", "192.168.1.10:8787", ":8787", "localhost:8787", "127.0.0.1"}
	for _, address := range invalid {
		t.Run(address, func(t *testing.T) {
			if err := validateListenAddress(address); err == nil {
				t.Fatal("expected non-loopback or invalid address to be rejected")
			}
		})
	}
}

func TestRemoveLegacyVault(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, "vault.json")
	if err := os.WriteFile(legacy, []byte("obsolete encrypted data"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := removeLegacyVault(dir); err != nil {
		t.Fatalf("remove legacy vault: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatalf("legacy vault still exists, stat error: %v", err)
	}
	if err := removeLegacyVault(dir); err != nil {
		t.Fatalf("missing legacy vault should be harmless: %v", err)
	}
}

func TestIsExistingDCCLite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/health" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"version":"test","api":"v1"}`))
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	if !isExistingDCCLite(address) {
		t.Fatal("did not recognize a running DCCLite health endpoint")
	}
}

func TestIsExistingDCCLiteRejectsOtherServices(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"version":"other","api":"v2"}`))
	}))
	defer server.Close()
	address := strings.TrimPrefix(server.URL, "http://")
	if isExistingDCCLite(address) {
		t.Fatal("mistook another local service for DCCLite")
	}
	if isExistingDCCLite("127.0.0.1:1") {
		t.Fatal("recognized a service that is not listening")
	}
}

func TestResolveDataDirMovesLegacyFolders(t *testing.T) {
	for _, old := range []string{"DCCNext", "BoxDeck"} {
		t.Run(old, func(t *testing.T) { checkLegacyMove(t, old) })
	}
}

func checkLegacyMove(t *testing.T, old string) {
	base := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", base)
	t.Setenv("APPDATA", base)
	t.Setenv("HOME", base)
	configDir, err := os.UserConfigDir()
	if err != nil {
		t.Skip("no user config dir on this platform")
	}
	legacy := filepath.Join(configDir, old)
	if err := os.MkdirAll(legacy, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "profiles.json"), []byte("[]"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir, err := resolveDataDir("", false)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dir) != "DCCLite" {
		t.Fatalf("data dir = %s", dir)
	}
	if _, err := os.Stat(filepath.Join(dir, "profiles.json")); err != nil {
		t.Fatalf("saved data was not carried over: %v", err)
	}
	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Fatal("old folder is still there")
	}
}
