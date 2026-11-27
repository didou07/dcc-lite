package httpapi

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func sh(t *testing.T, cmd string) string {
	t.Helper()
	out, err := exec.Command("sh", "-c", cmd).CombinedOutput()
	if err != nil {
		t.Fatalf("sh failed: %v\n%s\n%s", err, out, cmd)
	}
	return string(out)
}

func TestMovieMetaRoundTripKeepsOtherLines(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "20240101 1200 - It's - Show.ts")
	original := "1:0:19:283D:3FB:1:C00000:0:0:0:\nOld title\nOld desc\n1704110400\nold tag\n7200000\n123456\n\n188\n0\n"
	if err := os.WriteFile(file+".meta", []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	lines, exists := splitMetaLines(sh(t, movieMetaReadCommand(file)))
	if !exists || len(lines) != 10 {
		t.Fatalf("unexpected read: %v %v", lines, exists)
	}
	meta := parseMovieMeta(lines, exists)
	if meta.Title != "Old title" || meta.Tags != "old tag" || meta.Time != 1704110400 {
		t.Fatalf("unexpected meta: %+v", meta)
	}
	cmd, err := movieMetaWriteCommand(file, lines, exists, movieMetaRequest{File: file, Title: "Ahmed's \"new\" $title `x`", Description: "line one\nline two", Tags: "kids  Sport kids news"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sh(t, cmd), "SAVED") {
		t.Fatal("write did not report SAVED")
	}
	got, _ := os.ReadFile(file + ".meta")
	want := "1:0:19:283D:3FB:1:C00000:0:0:0:\nAhmed's \"new\" $title `x`\nline one line two\n1704110400\nkids Sport news\n7200000\n123456\n\n188\n0\n"
	if string(got) != want {
		t.Fatalf("meta mismatch:\n%q\nwant\n%q", got, want)
	}
	if _, err := os.Stat(file + ".meta.dcc"); err == nil {
		t.Error("temporary file was left behind")
	}
}

func TestMovieMetaCreatesMissingFile(t *testing.T) {
	file := filepath.Join(t.TempDir(), "rec.ts")
	lines, exists := splitMetaLines(sh(t, movieMetaReadCommand(file)))
	if exists {
		t.Fatal("meta should not exist")
	}
	ref := "1:0:0:0:0:0:0:0:0:0:" + file
	if _, err := movieMetaWriteCommand(file, lines, exists, movieMetaRequest{Title: "T"}); err == nil {
		t.Error("creating without a reference must fail")
	}
	cmd, err := movieMetaWriteCommand(file, lines, exists, movieMetaRequest{Ref: ref, Time: 5, Title: "T", Tags: "a"})
	if err != nil {
		t.Fatal(err)
	}
	sh(t, cmd)
	got, _ := os.ReadFile(file + ".meta")
	if string(got) != ref+"\nT\n\n5\na\n" {
		t.Fatalf("unexpected new meta: %q", got)
	}
}

func TestMovieMetaRejectsBadInput(t *testing.T) {
	for _, f := range []string{"", "rec.ts", "/media/hdd/../etc/passwd.ts", "/media/hdd/movie/", "/media/hdd/a.sh", "/media/hdd/a\n.ts", "/media/hdd/movie/x.ts.meta"} {
		if movieFileOK(f) {
			t.Errorf("accepted %q", f)
		}
	}
	if !movieFileOK("/media/hdd/movie/20240101 - It's.ts") {
		t.Error("rejected a normal recording")
	}
	if _, err := movieMetaWriteCommand("/media/hdd/a.ts", []string{"r", "t"}, true, movieMetaRequest{Title: "  "}); err == nil {
		t.Error("empty title accepted")
	}
	if _, err := movieMetaWriteCommand("/media/hdd/a.ts", make([]string, 30), true, movieMetaRequest{Title: "x"}); err == nil {
		t.Error("oversized meta accepted")
	}
	long := strings.Repeat("é", 600)
	if _, err := movieMetaWriteCommand("/media/hdd/a.ts", []string{"r", "t", "d", "1", "x"}, true, movieMetaRequest{Title: long, Description: long, Tags: long}); err != nil {
		t.Errorf("long text must be cut, not fail: %v", err)
	}
}

func TestMovieFolderCommands(t *testing.T) {
	base := filepath.Join(t.TempDir(), "movie")
	sh(t, "mkdir -p "+shellQuote(base))
	dir := base + "/Series 'A'"
	status := func(req movieFolderRequest) string {
		cmd, err := movieFolderCommand(req)
		if err != nil {
			t.Fatalf("%+v: %v", req, err)
		}
		return movieFolderStatus(sh(t, cmd))
	}
	if s := status(movieFolderRequest{Action: "create", Dir: dir + "/"}); s != "created" {
		t.Fatalf("create: %s", s)
	}
	if s := status(movieFolderRequest{Action: "create", Dir: dir}); s != "exists" {
		t.Fatalf("create twice: %s", s)
	}
	if s := status(movieFolderRequest{Action: "rename", Dir: dir, Name: "Shows"}); s != "renamed" {
		t.Fatalf("rename: %s", s)
	}
	moved := base + "/Shows"
	sh(t, "touch "+shellQuote(moved+"/x.ts"))
	if s := status(movieFolderRequest{Action: "remove", Dir: moved}); s != "notempty" {
		t.Fatalf("remove non-empty: %s", s)
	}
	sh(t, "rm "+shellQuote(moved+"/x.ts"))
	if s := status(movieFolderRequest{Action: "remove", Dir: moved}); s != "removed" {
		t.Fatalf("remove empty: %s", s)
	}
	if s := status(movieFolderRequest{Action: "remove", Dir: moved}); s != "missing" {
		t.Fatalf("remove missing: %s", s)
	}
}

func TestMovieFolderRejectsBadInput(t *testing.T) {
	bad := []movieFolderRequest{
		{Action: "create", Dir: "/"}, {Action: "create", Dir: "/media"}, {Action: "create", Dir: "/media/hdd"},
		{Action: "remove", Dir: "/media/../etc/x"}, {Action: "create", Dir: "media/hdd/x"}, {Action: "format", Dir: "/media/hdd/x"},
		{Action: "rename", Dir: "/media/hdd/movie/a", Name: "b/c"}, {Action: "rename", Dir: "/media/hdd/movie/a", Name: ".."},
		{Action: "rename", Dir: "/media/hdd/movie/a", Name: ""},
	}
	for _, req := range bad {
		if _, err := movieFolderCommand(req); err == nil {
			t.Errorf("accepted %+v", req)
		}
	}
}
