package httpapi

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"strings"
	"testing"
)

func makeTarGz(t *testing.T, entries []tar.Header, bodies map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, h := range entries {
		h := h
		body := bodies[h.Name]
		if h.Typeflag == tar.TypeReg {
			h.Size = int64(len(body))
		}
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			tw.Write([]byte(body))
		}
	}
	tw.Close()
	gz.Close()
	return buf.Bytes()
}

func TestReviewSettingsArchive(t *testing.T) {
	good := makeTarGz(t, []tar.Header{
		{Name: "etc/enigma2/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "etc/enigma2/settings", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "etc/tuxbox/config/oscam.server", Typeflag: tar.TypeReg, Mode: 0o600},
		{Name: "etc/CCcam.cfg", Typeflag: tar.TypeReg, Mode: 0o600},
	}, map[string]string{"etc/enigma2/settings": "a=b\n", "etc/tuxbox/config/oscam.server": "[reader]\n", "etc/CCcam.cfg": "N: x\n"})
	rev, err := reviewSettingsArchive(good)
	if err != nil || len(rev.Entries) != 3 || rev.Scopes["settings"] != 1 || rev.Scopes["softcam"] != 2 {
		t.Fatalf("good archive: %v %+v", err, rev)
	}
	bad := map[string][]tar.Header{
		"outside":   {{Name: "etc/passwd", Typeflag: tar.TypeReg}},
		"absolute":  {{Name: "/etc/enigma2/settings", Typeflag: tar.TypeReg}},
		"parent":    {{Name: "etc/enigma2/../passwd", Typeflag: tar.TypeReg}},
		"dotdot":    {{Name: "../etc/enigma2/x", Typeflag: tar.TypeReg}},
		"symlink":   {{Name: "etc/enigma2/x", Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}},
		"parentdir": {{Name: "etc/", Typeflag: tar.TypeDir, Mode: 0o777}},
		"odd name":  {{Name: "etc/enigma2/a;b", Typeflag: tar.TypeReg}},
	}
	for name, hs := range bad {
		if _, err := reviewSettingsArchive(makeTarGz(t, hs, nil)); err == nil {
			t.Errorf("%s: expected refusal", name)
		}
	}
	if _, err := reviewSettingsArchive([]byte("not gzip")); err == nil {
		t.Error("garbage accepted")
	}
}

func TestSettingsPackCommand(t *testing.T) {
	remote := "/tmp/dcc-settings-0123456789ab.tar.gz"
	cmd, err := settingsPackCommand("/", []string{"settings", "softcam"}, remote)
	if err != nil || !strings.Contains(cmd, "etc/enigma2") || !strings.Contains(cmd, "etc/tuxbox/config") || strings.Contains(cmd, "network/interfaces") || strings.Contains(cmd, "satellites.xml") || !strings.Contains(cmd, "ST=1") || !strings.Contains(cmd, "CH=0") {
		t.Fatalf("%v %s", err, cmd)
	}
	for _, bad := range [][]string{{"nope"}, {}} {
		if _, err := settingsPackCommand("/", bad, remote); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
	if _, err := settingsPackCommand("/", []string{"settings"}, "/tmp/x; rm -rf /"); err == nil {
		t.Error("accepted a bad remote path")
	}
	md5, size, err := parsePackOutput("rc=0\nd41d8cd98f00b204e9800998ecf8427e  /tmp/dcc-settings-0123456789ab.tar.gz\n1234\n")
	if err != nil || size != 1234 || md5 != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Fatalf("%v %v %v", md5, size, err)
	}
	if _, _, err := parsePackOutput("rc=1\n"); err == nil {
		t.Error("failed pack accepted")
	}
}

func TestSoftcamParseAndCommands(t *testing.T) {
	out := "init=/etc/init.d/softcam\ninit=/etc/init.d/softcam.oscam\ninit=/etc/init.d/softcam.ncam\ninit=/etc/init.d/softcam.oscam\nlink=/etc/init.d/softcam.oscam\n" +
		"@@ps\n 1234 root 12m S /usr/bin/oscam -b -c /etc/tuxbox/config\n@@files\nfile=/etc/tuxbox/config/oscam.server 812\nfile=/etc/passwd 5\n@@logs\nlog=/tmp/oscam.log 9000\nlog=/tmp/../etc/x.log 1\n"
	info := parseSoftcamInfo(out)
	if len(info.Cams) != 3 || info.Link != "/etc/init.d/softcam.oscam" || len(info.Running) != 1 || info.Running[0] != "oscam" ||
		len(info.Files) != 1 || len(info.Logs) != 1 {
		t.Fatalf("%+v", info)
	}
	if _, err := softcamActionCommand("restart", "softcam.oscam"); err != nil {
		t.Fatal(err)
	}
	if c, err := softcamActionCommand("switch", "softcam.ncam"); err != nil || !strings.Contains(c, "ln -sf") {
		t.Fatalf("%v %s", err, c)
	}
	for _, bad := range [][2]string{{"rm", ""}, {"start", "x;reboot"}, {"start", "../bin/sh"}, {"switch", ""}, {"start", "a b"}} {
		if _, err := softcamActionCommand(bad[0], bad[1]); err == nil {
			t.Errorf("accepted %v", bad)
		}
	}
}

func TestCleanSettingsArchiveDropsLinks(t *testing.T) {
	raw := makeTarGz(t, []tar.Header{
		{Name: "etc/tuxbox/config/", Typeflag: tar.TypeDir, Mode: 0o755},
		{Name: "etc/tuxbox/config/oscam.server", Typeflag: tar.TypeReg, Mode: 0o600},
		{Name: "etc/tuxbox/config/SoftCam.Key", Typeflag: tar.TypeSymlink, Linkname: "/var/keys/SoftCam.Key"},
		{Name: "var/keys", Typeflag: tar.TypeSymlink, Linkname: "/usr/keys"},
		{Name: "etc/enigma2/userbouquet.قنوات (HD).tv", Typeflag: tar.TypeReg, Mode: 0o644},
		{Name: "etc/enigma2/bad;name", Typeflag: tar.TypeReg, Mode: 0o644},
	}, map[string]string{"etc/tuxbox/config/oscam.server": "[reader]\n", "etc/enigma2/userbouquet.قنوات (HD).tv": "#NAME x\n", "etc/enigma2/bad;name": "x"})
	if _, err := reviewSettingsArchive(raw); err == nil {
		t.Fatal("the raw archive should be refused")
	}
	clean, skipped, err := cleanSettingsArchive(raw)
	if err != nil || skipped != 3 {
		t.Fatalf("clean: %v skipped=%d", err, skipped)
	}
	rev, err := reviewSettingsArchive(clean)
	if err != nil || len(rev.Entries) != 2 {
		t.Fatalf("cleaned archive refused: %v %+v", err, rev)
	}
	onlyLinks := makeTarGz(t, []tar.Header{{Name: "var/keys", Typeflag: tar.TypeSymlink, Linkname: "/usr/keys"}}, nil)
	if _, _, err := cleanSettingsArchive(onlyLinks); err == nil {
		t.Fatal("an archive with nothing restorable was accepted")
	}
}

func TestParsePackOutputNoFiles(t *testing.T) {
	if _, _, err := parsePackOutput("NOFILES\n"); err != errNoSettingsFiles {
		t.Fatalf("got %v", err)
	}
}

func TestPrepareSettingsUploadDropsLink(t *testing.T) {
	raw := makeTarGz(t, []tar.Header{
		{Name: "etc/tuxbox/config/oscam.conf", Typeflag: tar.TypeReg, Mode: 0o600},
		{Name: "etc/tuxbox/config/constant.cw-link", Typeflag: tar.TypeSymlink, Linkname: "/tmp/constant.cw"},
	}, map[string]string{"etc/tuxbox/config/oscam.conf": "[global]\n"})
	clean, skipped, err := prepareSettingsUpload(raw)
	if err != nil || skipped != 1 {
		t.Fatalf("%v skipped=%d", err, skipped)
	}
	rev, err := reviewSettingsArchive(clean)
	if err != nil || len(rev.Entries) != 1 || rev.Entries[0].Name != "etc/tuxbox/config/oscam.conf" {
		t.Fatalf("%v %+v", err, rev)
	}
	if _, _, err := prepareSettingsUpload([]byte("nope")); err == nil {
		t.Error("garbage accepted")
	}
	// Hostile entries are dropped, never applied.
	evil := makeTarGz(t, []tar.Header{
		{Name: "../etc/passwd", Typeflag: tar.TypeReg},
		{Name: "etc/shadow", Typeflag: tar.TypeReg},
	}, nil)
	if _, _, err := prepareSettingsUpload(evil); err == nil {
		t.Error("an archive with nothing allowed was accepted")
	}
}

func TestSoftcamDetectsRunningAndProgram(t *testing.T) {
	out := "init=/etc/init.d/softcam.oscam_emu\nbin=/etc/init.d/softcam.oscam_emu /usr/bin/oscam-emu\n" +
		"@@ps\n  812 root  45m S    /usr/bin/oscam-emu -b -r 2 -c /etc/tuxbox/config/oscam.conf\n  900 root  1m S    mgcamd\n@@files\n@@logs\n"
	info := parseSoftcamInfo(out)
	if info.Bins["softcam.oscam_emu"] != "/usr/bin/oscam-emu" {
		t.Fatalf("bins %+v", info.Bins)
	}
	if len(info.Running) != 2 || info.Running[0] != "oscam-emu" || info.Running[1] != "mgcamd" {
		t.Fatalf("running %+v", info.Running)
	}
}

func TestSettingsScopeSplitsChannelsFromSettings(t *testing.T) {
	cases := map[string]string{
		"etc/enigma2/lamedb":                    "channels",
		"etc/enigma2/bouquets.tv":               "channels",
		"etc/enigma2/userbouquet.قنوات (HD).tv": "channels",
		"etc/enigma2/alternatives.x.radio":      "channels",
		"etc/tuxbox/satellites.xml":             "channels",
		"etc/enigma2/settings":                  "settings",
		"etc/enigma2/timers.xml":                "settings",
		"etc/enigma2/skin/a.xml":                "settings",
		"etc/enigma2/skin/userbouquet.x.tv":     "settings",
		"etc/tuxbox/config/oscam.conf":          "softcam",
		"etc/CCcam.cfg":                         "softcam",
		"var/keys/SoftCam.Key":                  "softcam",
		"etc/network/interfaces":                "",
		"etc/passwd":                            "",
		"etc/tuxbox/config2/x":                  "",
	}
	for name, want := range cases {
		if got := settingsScopeOf(name); got != want {
			t.Errorf("%s: got %q, want %q", name, got, want)
		}
	}
}

func TestSettingsPackCommandPerScope(t *testing.T) {
	remote := "/tmp/dcc-settings-0123456789ab.tar.gz"
	ch, err := settingsPackCommand("/", []string{"channels"}, remote)
	if err != nil || !strings.Contains(ch, "CH=1") || !strings.Contains(ch, "ST=0") || !strings.Contains(ch, "satellites.xml") || strings.Contains(ch, "etc/CCcam.cfg") {
		t.Fatalf("channels: %v %s", err, ch)
	}
	sc, err := settingsPackCommand("/", []string{"softcam"}, remote)
	if err != nil || strings.Contains(sc, "etc/enigma2") || !strings.Contains(sc, "etc/CCcam.cfg") {
		t.Fatalf("softcam: %v %s", err, sc)
	}
	if _, err := settingsPackCommand("/", []string{"network"}, remote); err == nil {
		t.Error("the network scope no longer exists")
	}
}

func TestSoftcamFilesMatchBackupPaths(t *testing.T) {
	for _, p := range []string{"/etc/tuxbox/config/oscam.conf", "/var/tuxbox/config/oscam.server", "/etc/CCcam.cfg", "/var/etc/CCcam.cfg", "/var/keys/SoftCam.Key", "/usr/keys/SoftCam.Key"} {
		if !softcamFileRe.MatchString(p) {
			t.Errorf("%s should be listed", p)
		}
	}
	for _, p := range []string{"/etc/passwd", "/etc/tuxbox/config/../x", "/var/etc/other.cfg"} {
		if softcamFileRe.MatchString(p) {
			t.Errorf("%s must not be listed", p)
		}
	}
	out := parseSoftcamInfo("@@files\nfile=/var/etc/CCcam.cfg 12\nfile=/var/tuxbox/config/ncam.conf 40\nfile=/etc/shadow 1\n")
	if len(out.Files) != 2 {
		t.Fatalf("files: %+v", out.Files)
	}
}

func TestParseSoftcamInfoDetails(t *testing.T) {
	out := "init=/etc/init.d/softcam.oscam\nbin=/etc/init.d/softcam.oscam /usr/bin/oscam\ncfg=/etc/init.d/softcam.oscam /etc/tuxbox/config/\n" +
		"bin=/etc/init.d/softcam.evil /tmp/evil\ncfg=/etc/init.d/softcam.x /etc/../x\nlink=/etc/init.d/softcam.oscam\n" +
		"@@ps\n  812 root  21m S    /usr/bin/oscam -b -c /etc/tuxbox/config/ -r 2\n  900 root  0 S [camd_wq]\n  901 root 1m S grep oscam\n" +
		"@@boot\nrc=S50softcam /etc/init.d/softcam\nrc=S99ncam.sh\n@@files\n"
	info := parseSoftcamInfo(out)
	if info.Bins["softcam.oscam"] != "/usr/bin/oscam" || len(info.Bins) != 1 {
		t.Errorf("bins: %v", info.Bins)
	}
	if info.Confs["softcam.oscam"] != "/etc/tuxbox/config" || len(info.Confs) != 1 {
		t.Errorf("confs: %v", info.Confs)
	}
	if len(info.Procs) != 1 || info.Procs[0].PID != 812 || info.Procs[0].Name != "oscam" || info.Procs[0].Conf != "/etc/tuxbox/config" {
		t.Errorf("procs: %+v", info.Procs)
	}
	if len(info.Boot) != 2 || info.Boot[0] != "softcam" || info.Boot[1] != "ncam.sh" {
		t.Errorf("boot: %v", info.Boot)
	}
}
