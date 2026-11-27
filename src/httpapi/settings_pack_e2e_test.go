package httpapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func packOnFakeReceiver(t *testing.T, files []string, scopes []string) (map[string]string, settingsReview) {
	t.Helper()
	if _, err := exec.LookPath("tar"); err != nil {
		t.Skip("no tar")
	}
	root := t.TempDir()
	for _, rel := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte("x"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("oscam-emu", filepath.Join(root, "usr/bin/oscam")); err == nil {
		_ = err
	}
	remote := "/tmp/dcc-settings-0123456789ab.tar.gz"
	defer os.Remove(remote)
	cmd, err := settingsPackCommand(root, scopes, remote)
	if err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("%v %s", err, out)
	}
	if _, _, err := parsePackOutput(string(out)); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	raw, err := os.ReadFile(remote)
	if err != nil {
		t.Fatal(err)
	}
	clean, _, err := cleanSettingsArchive(raw)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := reviewSettingsArchive(clean)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, e := range rev.Entries {
		got[e.Name] = e.Scope
	}
	return got, rev
}

var fakeReceiverFiles = []string{
	"etc/tuxbox/config/oscam-emu/oscam.conf", "etc/tuxbox/config/ncam.conf", "etc/CCcam.cfg", "var/keys/SoftCam.Key",
	"etc/oscam/oscam.server", "etc/ncam/ncam.conf", "etc/oscam.conf", "etc/hosts", "usr/local/etc/oscam.conf", "usr/local/etc/unrelated.conf",
	"etc/enigma2/settings", "etc/enigma2/lamedb", "etc/enigma2/userbouquet.a.tv",
	"etc/init.d/softcam.oscam-emu", "etc/init.d/softcam.ncam-emu", "etc/init.d/networking",
	"usr/bin/oscam-emu", "usr/bin/ncam-emu", "usr/bin/enigma2", "usr/bin/mgcamd", "usr/softcams/oscam.arm", "var/emu/CCcam2.5",
}

func TestSettingsPackSoftcamConfigsOnly(t *testing.T) {
	got, _ := packOnFakeReceiver(t, fakeReceiverFiles, []string{"softcam"})
	for _, want := range []string{
		"etc/tuxbox/config/oscam-emu/oscam.conf", "etc/tuxbox/config/ncam.conf", "etc/CCcam.cfg", "var/keys/SoftCam.Key",
		"etc/oscam/oscam.server", "etc/ncam/ncam.conf", "etc/oscam.conf", "usr/local/etc/oscam.conf",
	} {
		if got[want] != "softcam" {
			t.Errorf("missing %s: %v", want, got)
		}
	}
	for name := range got {
		if got[name] != "softcam" || strings.HasPrefix(name, "usr/bin") || strings.HasPrefix(name, "etc/init.d") || name == "etc/hosts" || name == "usr/local/etc/unrelated.conf" {
			t.Errorf("unexpected %s (%s)", name, got[name])
		}
	}
}

func TestSettingsPackSoftcamPrograms(t *testing.T) {
	got, rev := packOnFakeReceiver(t, fakeReceiverFiles, []string{"emus"})
	var names []string
	for n, sc := range got {
		if sc != "emus" {
			t.Errorf("%s in scope %s", n, sc)
		}
		names = append(names, n)
	}
	sort.Strings(names)
	want := []string{"etc/init.d/softcam.ncam-emu", "etc/init.d/softcam.oscam-emu", "usr/bin/mgcamd", "usr/bin/ncam-emu", "usr/bin/oscam-emu", "usr/softcams/oscam.arm", "var/emu/CCcam2.5"}
	if strings.Join(names, ",") != strings.Join(want, ",") || rev.Scopes["emus"] != len(want) {
		t.Fatalf("got %v want %v", names, want)
	}
}

func TestSettingsPackAllScopesTogether(t *testing.T) {
	got, _ := packOnFakeReceiver(t, fakeReceiverFiles, settingsAllScopeIDs)
	for _, want := range []string{"etc/enigma2/lamedb", "etc/enigma2/settings", "etc/oscam/oscam.server", "usr/bin/oscam-emu"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing %s", want)
		}
	}
	for _, bad := range []string{"usr/bin/enigma2", "etc/init.d/networking", "etc/hosts"} {
		if _, ok := got[bad]; ok {
			t.Errorf("unexpected %s", bad)
		}
	}
}

func TestSettingsScopeOfSoftcamPrograms(t *testing.T) {
	cases := map[string]string{
		"usr/bin/oscam-emu": "emus", "usr/bin/enigma2": "", "usr/softcams/x/y": "emus", "etc/init.d/softcam.ncam": "emus", "etc/init.d/networking": "",
		"etc/oscam/oscam.conf": "softcam", "etc/oscam.conf": "softcam", "etc/passwd": "", "usr/bin/oscam/x": "", "etc/init.d/sub/softcam.x": "",
	}
	for name, want := range cases {
		if got := settingsScopeOf(name); got != want {
			t.Errorf("%s: got %q want %q", name, got, want)
		}
	}
}
