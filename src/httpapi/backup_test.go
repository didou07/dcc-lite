package httpapi

import (
	"archive/zip"
	"bytes"
	"strings"
	"testing"
)

func makeZip(t *testing.T, files map[string]string, order ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	names := order
	if len(names) == 0 {
		for n := range files {
			names = append(names, n)
		}
	}
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		w.Write([]byte(files[n]))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestIsChannelFile(t *testing.T) {
	good := []string{"lamedb", "lamedb5", "bouquets.tv", "bouquets.radio", "blacklist", "whitelist",
		"userbouquet.favourites.tv", "userbouquet.my+list.radio", "alternatives.x.tv"}
	bad := []string{"", "settings", "../lamedb", "a/lamedb", `..\lamedb`, "/etc/passwd", "lamedb\x00.tv",
		"userbouquet.x.txt", "userbouquet..", ".hidden", "bouquets.tv ", strings.Repeat("a", 200) + ".tv", "enigma2.sh"}
	for _, n := range good {
		if !isChannelFile(n) {
			t.Errorf("%q rejected", n)
		}
	}
	for _, n := range bad {
		if isChannelFile(n) {
			t.Errorf("%q accepted", n)
		}
	}
}

func TestRestoreRankOrdersListsBeforeIndexes(t *testing.T) {
	if !(restoreRank("lamedb") < restoreRank("userbouquet.a.tv") &&
		restoreRank("userbouquet.a.tv") < restoreRank("blacklist") &&
		restoreRank("blacklist") < restoreRank("bouquets.tv")) {
		t.Fatal("restore order must be lamedb, bouquet files, others, then the bouquet index last")
	}
}

func TestReadRestoreZipHappyPath(t *testing.T) {
	data := makeZip(t, map[string]string{
		"lamedb": "services", "bouquets.tv": "#NAME x", "userbouquet.a.tv": "#NAME a", backupManifest: "{}",
	})
	files, skipped, err := readRestoreZip(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 3 || len(skipped) != 0 {
		t.Fatalf("files=%d skipped=%v", len(files), skipped)
	}
	for _, f := range files {
		if f.Name == backupManifest {
			t.Fatal("the manifest must not be restored to the device")
		}
	}
}

func TestReadRestoreZipSkipsDangerousAndUnknownNames(t *testing.T) {
	data := makeZip(t, map[string]string{
		"lamedb": "ok", "../../etc/passwd": "evil", "/etc/shadow": "evil", `..\win.ini`: "evil",
		"settings": "x", "etc/enigma2/lamedb": "nested", "userbouquet.a.sh": "x",
	})
	files, skipped, err := readRestoreZip(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Name != "lamedb" {
		t.Fatalf("restored %+v", files)
	}
	if len(skipped) != 6 {
		t.Fatalf("skipped = %+v", skipped)
	}
}

func TestReadRestoreZipDuplicateNamesKeepFirst(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, body := range []string{"first", "second"} {
		w, _ := zw.Create("lamedb")
		w.Write([]byte(body))
	}
	zw.Close()
	files, skipped, err := readRestoreZip(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || string(files[0].Data) != "first" || len(skipped) != 1 || skipped[0].Reason != "listed twice" {
		t.Fatalf("files=%+v skipped=%+v", files, skipped)
	}
}

func TestReadRestoreZipRejectsBadInput(t *testing.T) {
	if _, _, err := readRestoreZip(nil); err == nil {
		t.Error("empty upload accepted")
	}
	if _, _, err := readRestoreZip([]byte("not a zip at all")); err == nil {
		t.Error("garbage accepted")
	}
	if _, _, err := readRestoreZip(makeZip(t, map[string]string{"userbouquet.a.tv": "x"})); err == nil {
		t.Error("a backup without lamedb/bouquets index accepted")
	}
	if _, _, err := readRestoreZip(makeZip(t, map[string]string{"readme.txt": "x"})); err == nil {
		t.Error("a zip with no channel files accepted")
	}
}

func TestReadRestoreZipLimits(t *testing.T) {
	huge := strings.Repeat("A", restoreMaxFile+10) // compresses to almost nothing: a zip bomb in miniature
	files, skipped, err := readRestoreZip(makeZip(t, map[string]string{"lamedb": "ok", "userbouquet.big.tv": huge}, "lamedb", "userbouquet.big.tv"))
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || len(skipped) != 1 || skipped[0].Reason != "file is too large" {
		t.Fatalf("files=%d skipped=%+v", len(files), skipped)
	}

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for i := 0; i <= restoreMaxList; i++ {
		zw.Create("junk")
	}
	zw.Close()
	if _, _, err = readRestoreZip(buf.Bytes()); err == nil {
		t.Error("an archive with too many entries accepted")
	}
}
