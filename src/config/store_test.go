package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func validProfile() Profile {
	return Profile{Name: "Living Room", Host: "192.168.1.20", Username: "root", Enabled: true}
}

func TestProfileSaveReloadAndDelete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := store.Save(validProfile())
	if err != nil {
		t.Fatal(err)
	}
	if len(saved.ID) != 32 {
		t.Fatalf("unexpected id %q", saved.ID)
	}
	if len(store.List()) != 1 {
		t.Fatal("expected one saved profile")
	}

	reloaded, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(saved.ID)
	if !ok || got.Host != saved.Host {
		t.Fatalf("profile was not persisted: %#v", got)
	}
	if err := reloaded.Delete(saved.ID); err != nil {
		t.Fatal(err)
	}
	if len(reloaded.List()) != 0 {
		t.Fatal("profile was not deleted")
	}
}

func TestProfileStorePersistsPasswordLocallyAndReloadsIt(t *testing.T) {
	dir := t.TempDir()
	store, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	profile := validProfile()
	profile.Password = DefaultPassword
	saved, err := store.Save(profile)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"password": "dreambox"`) {
		t.Fatalf("local profile file did not persist password: %s", data)
	}
	var publicJSON []byte
	publicJSON, err = json.Marshal(saved)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(publicJSON), "dreambox") || strings.Contains(string(publicJSON), "password") {
		t.Fatalf("Profile JSON must not expose its local password: %s", publicJSON)
	}
	var doc profileDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != SchemaVersion {
		t.Fatalf("schema = %d", doc.SchemaVersion)
	}
	reloaded, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := reloaded.Get(saved.ID)
	if !ok || got.Password != DefaultPassword {
		t.Fatalf("password was not reloaded from local profile file: %#v", got)
	}
}

func TestProfileStoreMigratesLegacySchemaToRootDreamboxDefaults(t *testing.T) {
	dir := t.TempDir()
	legacy := `{"schema_version":1,"profiles":[{"id":"legacy_12345678","name":"Legacy box","host":"192.168.1.20","port":80,"scheme":"http","enabled":true}]}`
	if err := os.WriteFile(filepath.Join(dir, "profiles.json"), []byte(legacy), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	profile, ok := store.Get("legacy_12345678")
	if !ok {
		t.Fatal("legacy profile missing after migration")
	}
	if profile.Username != DefaultUsername || profile.Password != DefaultPassword {
		t.Fatalf("legacy defaults = %q/%q, want root/dreambox", profile.Username, profile.Password)
	}
	data, err := os.ReadFile(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc profileDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.SchemaVersion != SchemaVersion {
		t.Fatalf("legacy profile was not migrated: schema=%d", doc.SchemaVersion)
	}
}

func TestProfileValidation(t *testing.T) {
	cases := []struct {
		name string
		edit func(*Profile)
	}{
		{"empty name", func(p *Profile) { p.Name = " " }},
		{"host path", func(p *Profile) { p.Host = "192.168.1.1/etc" }},
		{"invalid ftp port", func(p *Profile) { p.FTPPort = 70000 }},
		{"invalid web port", func(p *Profile) { p.Port = -1 }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validProfile()
			tc.edit(&p)
			if err := p.Validate(); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestCorruptStoreDoesNotSilentlyReset(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "profiles.json"), []byte("not-json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewProfileStore(dir); err == nil {
		t.Fatal("expected corrupt store error")
	}
}

func TestProfileFileIsPrivateOnUnix(t *testing.T) {
	if os.PathSeparator == '\\' {
		t.Skip("Unix permission bits are not applicable on Windows")
	}
	dir := t.TempDir()
	store, err := NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Save(validProfile()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, "profiles.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("profile file permissions are too broad: %o", info.Mode().Perm())
	}
}

func TestProfileImportCreatesIndependentIDs(t *testing.T) {
	store, err := NewProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	input := []Profile{validProfile(), Profile{Name: "Bedroom", Host: "192.168.1.21", Enabled: true}}
	imported, err := store.Import(input)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported) != 2 || imported[0].ID == "" || imported[1].ID == "" || imported[0].ID == imported[1].ID {
		t.Fatalf("unexpected imported profiles: %#v", imported)
	}
	if _, err := store.Import(input); err != nil {
		t.Fatal(err)
	}
	if len(store.List()) != 4 {
		t.Fatalf("re-import should add new profiles; got %d", len(store.List()))
	}
}

func TestProfileImportIsAtomicOnInvalidEntry(t *testing.T) {
	store, err := NewProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	input := []Profile{validProfile(), Profile{Name: "Bad", Host: "invalid/path"}}
	if _, err := store.Import(input); err == nil {
		t.Fatal("expected invalid profile rejection")
	}
	if len(store.List()) != 0 {
		t.Fatal("failed import partially changed profile store")
	}
}
