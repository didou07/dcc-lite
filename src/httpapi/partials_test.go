package httpapi

import (
	"os/exec"
	"strings"
	"testing"
)

func TestPartialPathOK(t *testing.T) {
	for _, p := range []string{
		"/.GTAV AR_XBOX JTAG.rar.dcclite-a594d137a1052144caaacf67c8c6e539.part",
		"/media/hdd/movie/.a.mkv.dcclite-1f.part",
		"/media/hdd/.a.mkv.dcclite-backup-1f.part",
	} {
		if !partialPathOK(p) {
			t.Errorf("should be accepted: %q", p)
		}
	}
	for _, p := range []string{
		"/etc/passwd", "/media/../etc/.a.dcclite-1.part", "/.a.dcclite-1.part\n", "/proc/1/.a.dcclite-1.part",
		"relative/.a.dcclite-1.part", "/a/b.dcclite-1.part", "/media/hdd/.a.dcclite-1.part; reboot", "",
	} {
		if partialPathOK(p) {
			t.Errorf("should be refused: %q", p)
		}
	}
}

func TestParsePartialScan(t *testing.T) {
	out := "f=477429760|/.GTAV AR_XBOX JTAG.rar.dcclite-a594d137a1052144caaacf67c8c6e539.part\nf=5|/proc/x\nf=1|/etc/passwd\nSCANDONE\n"
	files, noMmin, done := parsePartialScan(out)
	if noMmin || !done || len(files) != 1 || files[0].Size != 477429760 {
		t.Fatalf("unexpected result: %+v %v %v", files, noMmin, done)
	}
	if _, noMmin, _ := parsePartialScan("NOMMIN\n"); !noMmin {
		t.Error("NOMMIN must be reported")
	}
}

func TestPartialCommandsAreShellValid(t *testing.T) {
	paths := []string{"/.GTAV AR_XBOX JTAG.rar.dcclite-a1.part", "/media/hdd/it's/.a.dcclite-2.part"}
	cmds := map[string]string{"scan": partialScanCommand, "delete": partialDeleteCommand(paths)}
	for name, cmd := range cmds {
		if strings.ContainsAny(cmd, "\r\n\x00") || len(cmd) > 5800 {
			t.Errorf("%s: not a safe single line (%d bytes)", name, len(cmd))
		}
		if out, err := exec.Command("sh", "-n", "-c", cmd).CombinedOutput(); err != nil {
			t.Errorf("%s: invalid shell: %v %s", name, err, out)
		}
	}
	var many []string
	for i := 0; i < 100; i++ {
		many = append(many, "/media/hdd/some/long/folder/name/.a-long-file-name-for-testing.mkv.dcclite-0123456789abcdef0123456789abcdef.part")
	}
	for _, chunk := range partialChunks(many) {
		if cmd := partialDeleteCommand(chunk); len(cmd) > 5800 {
			t.Errorf("chunk too long: %d", len(cmd))
		}
	}
}
