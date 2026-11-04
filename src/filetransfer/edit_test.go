package filetransfer

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

func newEditManager(t *testing.T) (*Manager, *memoryTransfer) {
	t.Helper()
	manager := NewManager()
	remote := newMemoryTransfer()
	remote.dirs["/etc"] = true
	manager.sessions["s1"] = &managedSession{
		info:    SessionInfo{ID: "s1", ProfileID: "p1", Protocol: "ftp", Host: "box", Port: 21},
		adapter: remote,
	}
	return manager, remote
}

func remoteFile(t *testing.T, remote *memoryTransfer, name string) (string, bool) {
	t.Helper()
	remote.mu.Lock()
	defer remote.mu.Unlock()
	data, ok := remote.files[name]
	return string(data), ok
}

func remoteLeftovers(remote *memoryTransfer) []string {
	remote.mu.Lock()
	defer remote.mu.Unlock()
	var names []string
	for name := range remote.files {
		if strings.Contains(name, ".dccnext-") {
			names = append(names, name)
		}
	}
	return names
}

func TestReadTextReturnsContentAndMetadata(t *testing.T) {
	manager, remote := newEditManager(t)
	remote.files["/etc/hostname"] = []byte("vuduo4k\n")
	remote.modes["/etc/hostname"] = 0o644
	file, err := manager.ReadText(context.Background(), "s1", "/etc/hostname")
	if err != nil {
		t.Fatalf("ReadText: %v", err)
	}
	if file.Content != "vuduo4k\n" || file.Size != 8 || file.LineEnding != LineEndingLF || file.Mode != "0644" {
		t.Fatalf("unexpected text file: %#v", file)
	}
}

func TestReadTextNormalisesCRLFAndReportsMixed(t *testing.T) {
	manager, remote := newEditManager(t)
	remote.files["/etc/dos.conf"] = []byte("a=1\r\nb=2\r\n")
	remote.files["/etc/mixed.conf"] = []byte("a=1\r\nb=2\n")
	dos, err := manager.ReadText(context.Background(), "s1", "/etc/dos.conf")
	if err != nil || dos.Content != "a=1\nb=2\n" || dos.LineEnding != LineEndingCRLF {
		t.Fatalf("crlf file = %#v, err %v", dos, err)
	}
	mixed, err := manager.ReadText(context.Background(), "s1", "/etc/mixed.conf")
	if err != nil || mixed.Content != "a=1\nb=2\n" || mixed.LineEnding != LineEndingMixed {
		t.Fatalf("mixed file = %#v, err %v", mixed, err)
	}
}

func TestReadTextRejectsBinaryLargeAndDirectories(t *testing.T) {
	manager, remote := newEditManager(t)
	remote.files["/etc/blob"] = []byte{0x7f, 'E', 'L', 'F', 0, 1, 2}
	remote.files["/etc/latin1"] = []byte{'c', 'a', 'f', 0xe9}
	remote.files["/etc/huge"] = []byte(strings.Repeat("x", MaxEditBytes+1))
	cases := map[string]error{
		"/etc/blob":    ErrEditNotText,
		"/etc/latin1":  ErrEditNotText,
		"/etc/huge":    ErrEditTooLarge,
		"/etc":         ErrEditDirectory,
		"/etc/missing": ErrEditNotFound,
	}
	for name, want := range cases {
		if _, err := manager.ReadText(context.Background(), "s1", name); !errors.Is(err, want) {
			t.Errorf("ReadText(%s) error = %v, want %v", name, err, want)
		}
	}
}

func TestWriteTextReplacesFilePreservesModeAndLeavesNoPartial(t *testing.T) {
	manager, remote := newEditManager(t)
	remote.files["/etc/rc.local"] = []byte("old\n")
	remote.modes["/etc/rc.local"] = 0o755
	opened, err := manager.ReadText(context.Background(), "s1", "/etc/rc.local")
	if err != nil {
		t.Fatal(err)
	}
	saved, err := manager.WriteText(context.Background(), "s1", "/etc/rc.local", TextWrite{
		Content: "new\nline\n", LineEnding: LineEndingLF, ExpectedSize: opened.Size, ExpectedModifiedAt: opened.ModifiedAt,
	})
	if err != nil {
		t.Fatalf("WriteText: %v", err)
	}
	if got, _ := remoteFile(t, remote, "/etc/rc.local"); got != "new\nline\n" {
		t.Fatalf("remote content = %q", got)
	}
	remote.mu.Lock()
	mode := remote.modes["/etc/rc.local"]
	remote.mu.Unlock()
	if mode != 0o755 {
		t.Fatalf("mode = %04o, want 0755", mode)
	}
	if saved.Size != int64(len("new\nline\n")) {
		t.Fatalf("saved size = %d", saved.Size)
	}
	if left := remoteLeftovers(remote); len(left) != 0 {
		t.Fatalf("partial/backup files left behind: %v", left)
	}
}

func TestWriteTextRestoresCRLF(t *testing.T) {
	manager, remote := newEditManager(t)
	remote.files["/etc/dos.conf"] = []byte("a=1\r\n")
	opened, _ := manager.ReadText(context.Background(), "s1", "/etc/dos.conf")
	if _, err := manager.WriteText(context.Background(), "s1", "/etc/dos.conf", TextWrite{
		Content: "a=1\nb=2\n", LineEnding: opened.LineEnding, ExpectedSize: opened.Size,
	}); err != nil {
		t.Fatal(err)
	}
	if got, _ := remoteFile(t, remote, "/etc/dos.conf"); got != "a=1\r\nb=2\r\n" {
		t.Fatalf("remote content = %q", got)
	}
}

func TestWriteTextDetectsRemoteChangeUnlessForced(t *testing.T) {
	manager, remote := newEditManager(t)
	remote.files["/etc/settings"] = []byte("v1\n")
	opened, _ := manager.ReadText(context.Background(), "s1", "/etc/settings")
	remote.mu.Lock()
	remote.files["/etc/settings"] = []byte("someone else changed this\n")
	remote.mu.Unlock()
	write := TextWrite{Content: "mine\n", LineEnding: LineEndingLF, ExpectedSize: opened.Size, ExpectedModifiedAt: opened.ModifiedAt}
	if _, err := manager.WriteText(context.Background(), "s1", "/etc/settings", write); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("error = %v, want ErrEditConflict", err)
	}
	if got, _ := remoteFile(t, remote, "/etc/settings"); got != "someone else changed this\n" {
		t.Fatalf("conflicting save modified the file: %q", got)
	}
	write.Force = true
	if _, err := manager.WriteText(context.Background(), "s1", "/etc/settings", write); err != nil {
		t.Fatalf("forced save: %v", err)
	}
	if got, _ := remoteFile(t, remote, "/etc/settings"); got != "mine\n" {
		t.Fatalf("forced content = %q", got)
	}
}

func TestWriteTextCreateAndMissingRules(t *testing.T) {
	manager, remote := newEditManager(t)
	remote.files["/etc/exists"] = []byte("x")
	ctx := context.Background()
	if _, err := manager.WriteText(ctx, "s1", "/etc/new.txt", TextWrite{Content: "hello\n", Create: true}); err != nil {
		t.Fatalf("create: %v", err)
	}
	if got, ok := remoteFile(t, remote, "/etc/new.txt"); !ok || got != "hello\n" {
		t.Fatalf("created file = %q, %v", got, ok)
	}
	if _, err := manager.WriteText(ctx, "s1", "/etc/exists", TextWrite{Content: "y", Create: true}); !errors.Is(err, ErrEditExists) {
		t.Fatalf("create over existing error = %v", err)
	}
	if _, err := manager.WriteText(ctx, "s1", "/etc/gone", TextWrite{Content: "y"}); !errors.Is(err, ErrEditNotFound) {
		t.Fatalf("save of missing file error = %v", err)
	}
	if _, err := manager.WriteText(ctx, "s1", "/etc/new.txt", TextWrite{Content: "a\x00b", Force: true}); !errors.Is(err, ErrEditNotText) {
		t.Fatalf("NUL content error = %v", err)
	}
	if left := remoteLeftovers(remote); len(left) != 0 {
		t.Fatalf("partial files left behind: %v", left)
	}
}

func TestSameRemoteVersion(t *testing.T) {
	now := time.Date(2026, 11, 1, 12, 0, 0, 0, time.UTC)
	entry := core.FileEntry{Size: 10, ModifiedAt: now}
	if !sameRemoteVersion(entry, 10, now.Add(300*time.Millisecond)) {
		t.Error("sub-second differences must be ignored")
	}
	if sameRemoteVersion(entry, 11, now) {
		t.Error("size change must be a conflict")
	}
	if sameRemoteVersion(entry, 10, now.Add(time.Minute)) {
		t.Error("mtime change must be a conflict")
	}
	if !sameRemoteVersion(core.FileEntry{Size: 10}, 10, now) {
		t.Error("a listing without mtime falls back to size only")
	}
}

func TestWriteTextFlagsUnknownPermissions(t *testing.T) {
	manager, remote := newEditManager(t)
	remote.files["/etc/run.sh"] = []byte("old\n")
	remote.modes["/etc/run.sh"] = 0o755
	// A server whose listing carries no mode information.
	manager.sessions["s1"].adapter = &noModeTransfer{memoryTransfer: remote}
	saved, err := manager.WriteText(context.Background(), "s1", "/etc/run.sh", TextWrite{Content: "new\n", Force: true})
	if err != nil {
		t.Fatal(err)
	}
	if !saved.ModeUnknown {
		t.Fatal("a save that could not preserve permissions must say so")
	}
}

type noModeTransfer struct{ *memoryTransfer }

func (n *noModeTransfer) List(ctx context.Context, dir string) ([]core.FileEntry, error) {
	entries, err := n.memoryTransfer.List(ctx, dir)
	for i := range entries {
		entries[i].Mode = ""
	}
	return entries, err
}

func TestWriteTextDetectsSameSizeSameTimeChangeByHash(t *testing.T) {
	manager, remote := newEditManager(t)
	remote.files["/etc/settings"] = []byte("aaaa\n")
	opened, err := manager.ReadText(context.Background(), "s1", "/etc/settings")
	if err != nil || opened.Hash == "" {
		t.Fatalf("read: %v hash=%q", err, opened.Hash)
	}
	// Same length, and the fake listing has no mtime: only the hash can tell.
	remote.mu.Lock()
	remote.files["/etc/settings"] = []byte("bbbb\n")
	remote.mu.Unlock()
	write := TextWrite{Content: "mine\n", ExpectedSize: opened.Size, ExpectedModifiedAt: opened.ModifiedAt, ExpectedHash: opened.Hash}
	if _, err := manager.WriteText(context.Background(), "s1", "/etc/settings", write); !errors.Is(err, ErrEditConflict) {
		t.Fatalf("error = %v, want ErrEditConflict", err)
	}
	if got, _ := remoteFile(t, remote, "/etc/settings"); got != "bbbb\n" {
		t.Fatalf("conflicting save modified the file: %q", got)
	}
	// An unchanged file saves, and the returned hash chains into the next save.
	remote.mu.Lock()
	remote.files["/etc/settings"] = []byte("aaaa\n")
	remote.mu.Unlock()
	saved, err := manager.WriteText(context.Background(), "s1", "/etc/settings", write)
	if err != nil {
		t.Fatalf("save of unchanged file: %v", err)
	}
	write.Content, write.ExpectedHash, write.ExpectedSize = "mine again\n", saved.Hash, saved.Size
	if _, err := manager.WriteText(context.Background(), "s1", "/etc/settings", write); err != nil {
		t.Fatalf("second save with chained hash: %v", err)
	}
}
