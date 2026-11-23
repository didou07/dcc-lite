package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestParseProcMTD(t *testing.T) {
	out := "dev:    size   erasesize  name\n" +
		"mtd0: 00800000 00020000 \"complete\"\n" +
		"mtd1: 00040000 00020000 \"bootloader\"\n" +
		"mtd2: 007c0000 00020000 \"root fs\"\r\n" +
		"junk line\nmtd3: zz 00020000 \"bad\"\n"
	got := parseProcMTD(out)
	if len(got) != 3 {
		t.Fatalf("partitions: %+v", got)
	}
	if got[0].Index != 0 || got[0].Size != 8<<20 || got[0].EraseSize != 0x20000 || got[0].Name != "complete" {
		t.Fatalf("first: %+v", got[0])
	}
	if got[2].Device != "mtd2" || got[2].Name != "root fs" || got[2].Size != 0x7c0000 {
		t.Fatalf("third: %+v", got[2])
	}
}

func TestParseFreeKB(t *testing.T) {
	plain := "Filesystem           1K-blocks      Used Available Use% Mounted on\ntmpfs                    30000      1200     28800   4% /tmp\n"
	if free, ok := parseFreeKB(plain); !ok || free != 28800 {
		t.Fatalf("plain: %d %v", free, ok)
	}
	wrapped := "Filesystem           1K-blocks      Used Available Use% Mounted on\n/dev/some/very/long/device/name\n                         30000      1200     28800   4% /tmp\n"
	if free, ok := parseFreeKB(wrapped); !ok || free != 28800 {
		t.Fatalf("wrapped: %d %v", free, ok)
	}
	if _, ok := parseFreeKB("df: not found"); ok {
		t.Fatal("garbage was accepted")
	}
}

func TestParseMD5(t *testing.T) {
	if got := parseMD5("d41d8cd98f00b204e9800998ecf8427e  /tmp/x.bin\n"); got != "d41d8cd98f00b204e9800998ecf8427e" {
		t.Fatalf("got %q", got)
	}
	if parseMD5("md5sum: not found") != "" {
		t.Fatal("error text taken as a checksum")
	}
}

func TestFlashDumpCommandIsReadOnlyAndStrict(t *testing.T) {
	path, err := flashTempPath()
	if err != nil || !validFlashTempPath(path) {
		t.Fatalf("temp path %q: %v", path, err)
	}
	cmd, err := flashDumpCommand(2, path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(cmd, "cat \"$src\" > '"+path+"'") || !strings.Contains(cmd, "/dev/mtd/2") {
		t.Fatalf("command: %s", cmd)
	}
	// Nothing in it may write to a flash device.
	for _, bad := range []string{"> /dev/", "of=/dev", "eraseall", "flash_erase", "nandwrite", "cp /tmp"} {
		if strings.Contains(cmd, bad) {
			t.Fatalf("command can write to flash (%s): %s", bad, cmd)
		}
	}
	for _, bad := range []struct {
		n    int
		path string
	}{{-1, path}, {32, path}, {1, "/tmp/x.bin"}, {1, "/etc/passwd"}, {1, "/tmp/dcc-flash-../../etc.bin"}, {1, "/tmp/dcc-flash-0000000000000000.bin; rm -rf /"}} {
		if _, err := flashDumpCommand(bad.n, bad.path); err == nil {
			t.Errorf("accepted %d %q", bad.n, bad.path)
		}
	}
}

func TestSafeFileWord(t *testing.T) {
	if got := safeFileWord(`root fs/../"x"`); strings.ContainsAny(got, ` /"`) || got == "" {
		t.Fatalf("got %q", got)
	}
	if safeFileWord("???") != "partition" {
		t.Fatal("empty name not replaced")
	}
}

// In a remote session the flash routes refuse before touching any receiver.
func TestFlashRoutesAreRefusedInRemoteSessions(t *testing.T) {
	s := &Server{remote: &remoteMode{}}
	for name, handler := range map[string]http.HandlerFunc{"partitions": s.flashPartitions, "backup": s.flashBackup} {
		rec := httptest.NewRecorder()
		handler(rec, httptest.NewRequest("POST", "/api/v1/profiles/x/flash/"+name+"?partition=0", nil))
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "flash_unavailable_remote") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body.String())
		}
	}
	local := &Server{}
	if local.denyFlashRemote(httptest.NewRecorder()) {
		t.Error("flash was refused on the local computer")
	}
}
