package httpapi

import (
	"strings"
	"testing"
)

func TestSoftcamProcScanParserAndWebConfig(t *testing.T) {
	out := "init=/etc/init.d/softcam.oscam_emu\n" +
		"bin=/etc/init.d/softcam.oscam_emu /usr/bin/oscam-emu\n" +
		"cfg=/etc/init.d/softcam.oscam_emu /etc/tuxbox/config\n" +
		"link=/etc/init.d/softcam.oscam_emu\n" +
		"@@ps\n" +
		"proc=733 /usr/bin/oscam-emu -b -c /etc/tuxbox/config\n" +
		"proc=811 /usr/bin/mgcamd\n" +
		"@@web\n" +
		"webf=/etc/tuxbox/config/oscam.conf\n" +
		"kv=httpport=+8888\n" +
		"kv=logfile=/tmp/oscam/oscam.log\n" +
		"webf=/etc/tuxbox/config/oscam.conf\n" +
		"kv=httpport=+8888\n" +
		"@@boot\nrc=S50softcam /etc/init.d/softcam\n@@files\n@@logs\n"

	info := parseSoftcamInfo(out)
	if len(info.Procs) != 2 {
		t.Fatalf("processes: %+v", info.Procs)
	}
	if info.Procs[0].PID != 733 || info.Procs[0].Name != "oscam-emu" || info.Procs[0].Conf != "/etc/tuxbox/config" || !strings.Contains(info.Procs[0].Cmd, " -b ") {
		t.Fatalf("oscam process: %+v", info.Procs[0])
	}
	if info.Procs[1].Name != "mgcamd" {
		t.Fatalf("mgcamd process: %+v", info.Procs[1])
	}
	if len(info.Web) != 1 || info.Web[0].Port != 8888 || !info.Web[0].TLS {
		t.Fatalf("web interface: %+v", info.Web)
	}
	if info.Web[0].Log != "/tmp/oscam/oscam.log" || len(info.Logs) != 1 || info.Logs[0].Path != info.Web[0].Log {
		t.Fatalf("log discovery: web=%+v logs=%+v", info.Web, info.Logs)
	}
}

func TestSoftcamLogPathValidationAndIncrementalParser(t *testing.T) {
	for _, path := range []string{"/tmp/oscam.log", "/tmp/oscam/oscam.log", "/var/log/softcam/oscam/main.log"} {
		if !softcamLogOK(path) {
			t.Errorf("expected valid log path: %s", path)
		}
	}
	for _, path := range []string{"/tmp/../etc/passwd.log", "/etc/passwd", "/tmp/a b.log", "/tmp/a;reboot.log"} {
		if softcamLogOK(path) {
			t.Errorf("accepted invalid log path: %s", path)
		}
	}

	text, size, reset, missing := parseSoftcamLog("@@size=42\nnew line\n")
	if text != "new line\n" || size != 42 || reset || missing {
		t.Fatalf("incremental output: text=%q size=%d reset=%v missing=%v", text, size, reset, missing)
	}
	text, size, reset, missing = parseSoftcamLog("@@size=7\n@@reset\nreplacement\n")
	if text != "replacement\n" || size != 7 || !reset || missing {
		t.Fatalf("rotated output: text=%q size=%d reset=%v missing=%v", text, size, reset, missing)
	}
	_, _, _, missing = parseSoftcamLog("@@missing\n")
	if !missing {
		t.Fatal("missing log marker was not recognized")
	}
}

func TestSoftcamKillCommandIsPIDBoundAndChecked(t *testing.T) {
	command, err := softcamKillCommand([]int{733, 811})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"softcam_is_cam", "for P in 733 811", "kill -TERM", "kill -KILL", `echo "@@ps"`} {
		if !strings.Contains(command, want) {
			t.Errorf("kill command missing %q", want)
		}
	}
	for _, pids := range [][]int{nil, {1}, {-10}, {4194305}} {
		if _, err := softcamKillCommand(pids); err == nil {
			t.Errorf("accepted invalid PID list: %v", pids)
		}
	}
	all := softcamKillAllCommand()
	if !strings.Contains(all, "/proc/[0-9]*") || !strings.Contains(all, "softcam_is_cam") {
		t.Fatalf("kill-all command is not process-scoped: %s", all)
	}
}

func TestSoftcamScanUsesProcAndMatchesPrefixedCamNames(t *testing.T) {
	command := softcamScan(true)
	for _, want := range []string{"/proc/[0-9]*", "readlink", "proc=$P", "DIRS=", "--config-dir"} {
		if !strings.Contains(command, want) {
			t.Errorf("scan command missing %q", want)
		}
	}
	pattern := softcamCase()
	if !strings.Contains(pattern, "*[Cc][Aa][Mm][Dd]*") || !strings.Contains(pattern, "[Oo][Ss][Cc][Aa][Mm]*") || strings.Contains(pattern, "*[Oo][Ss][Cc][Aa][Mm]*") {
		t.Fatalf("process patterns do not match camd prefixes safely: %s", pattern)
	}
}

func TestSoftcamListeningPortsAndSubfolderConfigs(t *testing.T) {
	out := "@@ps\n" +
		"proc=733 /usr/bin/oscam-emu -b\n" +
		"listen=733 8899,34000\n" +
		"@@web\n" +
		"webf=/etc/tuxbox/config/oscam.conf\n" +
		"kv=httpport=8888\n" +
		"webf=/etc/tuxbox/config/oscam-emu/oscam.conf\n" +
		"kv=httpport=8899\n"
	info := parseSoftcamInfo(out)
	if len(info.Procs) != 1 || len(info.Procs[0].Ports) != 2 || info.Procs[0].Ports[0] != 8899 {
		t.Fatalf("listening ports: %+v", info.Procs)
	}
	if len(info.Web) != 2 || info.Web[1].Port != 8899 {
		t.Fatalf("web interfaces: %+v", info.Web)
	}
	for _, want := range []string{"/etc/tuxbox/config/*", "/proc/net/tcp", `echo "listen=`} {
		if !strings.Contains(softcamInfoCommand, want) {
			t.Errorf("info command missing %q", want)
		}
	}
}
