package filetransfer

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLocalFSConfinesPathsToRoot(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	fs := &LocalFS{root: root}
	for _, value := range []string{"../secret", "../../etc/passwd", "/etc/passwd"} {
		if _, _, err := fs.Resolve(value, false); err == nil {
			t.Fatalf("accepted unsafe path %q", value)
		}
	}
	if _, _, err := fs.Resolve("outside-link/secret", false); err == nil {
		t.Fatal("followed symlink outside local root")
	}
	if _, _, err := fs.Resolve("sub", false); err != nil {
		t.Fatalf("valid in-root path rejected: %v", err)
	}
}

func TestLocalFSOperationsAndRootProtection(t *testing.T) {
	root := t.TempDir()
	fs := &LocalFS{root: root}
	if err := fs.Mkdir("folder"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "folder", "old.txt"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	entries, current, err := fs.List("folder")
	if err != nil || current != "folder" || len(entries) != 1 || entries[0].Name != "old.txt" {
		t.Fatalf("list = %#v, %q, %v", entries, current, err)
	}
	if err := fs.Rename("folder/old.txt", "folder/new.txt"); err != nil {
		t.Fatal(err)
	}
	if err := fs.Rename("folder/new.txt", "folder/new.txt"); err == nil {
		t.Fatal("rename overwrote an existing file")
	}
	if err := fs.Chmod("folder/new.txt", 0o640); err != nil {
		t.Fatal(err)
	}
	if err := fs.Remove("."); err == nil {
		t.Fatal("removed the local root")
	}
	if err := fs.Remove("folder/new.txt"); err != nil {
		t.Fatal(err)
	}
}

func TestLocalFSReplaceTempHonorsOverwrite(t *testing.T) {
	root := t.TempDir()
	fs := &LocalFS{root: root}
	target := filepath.Join(root, "file.txt")
	tmp := filepath.Join(root, "tmp.part")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fs.ReplaceTemp(tmp, "file.txt", false); err == nil {
		t.Fatal("replaced file without overwrite consent")
	}
	if err := fs.ReplaceTemp(tmp, "file.txt", true); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "new" {
		t.Fatalf("replacement content = %q, %v", data, err)
	}
}

func TestLocalFSEnsureDirCreatesNestedDirectoriesSafely(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	fs := &LocalFS{root: root}

	if err := fs.EnsureDir("new/deep/nested"); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	if info, err := os.Stat(filepath.Join(root, "new", "deep", "nested")); err != nil || !info.IsDir() {
		t.Fatalf("nested directory was not created: info=%v err=%v", info, err)
	}

	if err := os.Symlink(outside, filepath.Join(root, "outside-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := fs.EnsureDir("outside-link/should-not-exist"); err == nil {
		t.Fatal("created a directory through a symlink that escapes the permitted root")
	}
	if _, err := os.Stat(filepath.Join(outside, "should-not-exist")); !os.IsNotExist(err) {
		t.Fatalf("outside directory was unexpectedly created: %v", err)
	}

	if err := os.WriteFile(filepath.Join(root, "not-a-folder"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := fs.EnsureDir("not-a-folder/child"); err == nil {
		t.Fatal("accepted a regular file as a directory component")
	}
}
