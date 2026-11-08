package filetransfer

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

type LocalFS struct{ root string }

func NewLocalFS() (*LocalFS, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve user home directory: %w", err)
	}
	root, err := filepath.EvalSymlinks(home)
	if err != nil {
		return nil, fmt.Errorf("resolve local root: %w", err)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil, errors.New("local file root is not a directory")
	}
	return &LocalFS{root: filepath.Clean(root)}, nil
}

// NewLocalFSAt roots a LocalFS at an explicit directory (created when missing).
// DCC Lite uses it for the private staging folder that carries files between
// the browser and the receiver.
func NewLocalFSAt(dir string) (*LocalFS, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}
	root, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, fmt.Errorf("resolve staging directory: %w", err)
	}
	return &LocalFS{root: filepath.Clean(root)}, nil
}

// Root is the absolute directory this LocalFS is confined to.
func (f *LocalFS) Root() string { return f.root }

func (f *LocalFS) RootLabel() string { return "Home" }
func (f *LocalFS) List(relative string) ([]core.FileEntry, string, error) {
	absolute, clean, err := f.Resolve(relative, false)
	if err != nil {
		return nil, "", err
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return nil, "", fmt.Errorf("read local directory: %w", err)
	}
	out := make([]core.FileEntry, 0, len(entries))
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		mode := info.Mode().String()
		out = append(out, core.FileEntry{Name: entry.Name(), Path: joinLocal(clean, entry.Name()), Size: info.Size(), ModifiedAt: info.ModTime().UTC(), IsDir: info.IsDir(), Mode: mode})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, clean, nil
}
func (f *LocalFS) Resolve(relative string, allowMissing bool) (string, string, error) {
	clean, err := f.cleanRelativePath(relative)
	if err != nil {
		return "", "", err
	}
	converted := filepath.FromSlash(clean)
	candidate := filepath.Join(f.root, converted)
	if !withinRoot(f.root, candidate) {
		return "", "", errors.New("local path escapes the permitted home directory")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if !allowMissing || !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		parent := filepath.Dir(candidate)
		resolvedParent, parentErr := filepath.EvalSymlinks(parent)
		if parentErr != nil {
			return "", "", parentErr
		}
		if !withinRoot(f.root, resolvedParent) {
			return "", "", errors.New("local path resolves outside the permitted home directory")
		}
		resolved = filepath.Join(resolvedParent, filepath.Base(candidate))
	} else if !withinRoot(f.root, resolved) {
		return "", "", errors.New("local symlink resolves outside the permitted home directory")
	}
	return resolved, clean, nil
}

func (f *LocalFS) cleanRelativePath(relative string) (string, error) {
	if len(relative) > 4096 || strings.ContainsAny(relative, "\x00\r\n") {
		return "", errors.New("local path is invalid")
	}
	relative = strings.ReplaceAll(relative, "\\", "/")
	if strings.HasPrefix(relative, "/") || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		return "", errors.New("absolute local paths are not allowed")
	}
	clean := pathClean(relative)
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("local path escapes the permitted home directory")
	}
	return clean, nil
}

// EnsureDir creates missing path components one at a time. This avoids
// os.MkdirAll following an unchecked symlink outside the permitted root.
func (f *LocalFS) EnsureDir(relative string) error {
	clean, err := f.cleanRelativePath(relative)
	if err != nil {
		return err
	}
	if clean == "." {
		return nil
	}
	current := f.root
	for _, component := range strings.Split(clean, "/") {
		if component == "" || component == "." {
			continue
		}
		candidate := filepath.Join(current, filepath.FromSlash(component))
		if !withinRoot(f.root, candidate) {
			return errors.New("local path escapes the permitted home directory")
		}
		info, statErr := os.Lstat(candidate)
		if errors.Is(statErr, os.ErrNotExist) {
			if mkdirErr := os.Mkdir(candidate, 0o700); mkdirErr != nil && !errors.Is(mkdirErr, os.ErrExist) {
				return fmt.Errorf("create local directory: %w", mkdirErr)
			}
			info, statErr = os.Lstat(candidate)
		}
		if statErr != nil {
			return statErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			resolved, resolveErr := filepath.EvalSymlinks(candidate)
			if resolveErr != nil {
				return resolveErr
			}
			if !withinRoot(f.root, resolved) {
				return errors.New("local symlink resolves outside the permitted home directory")
			}
			info, statErr = os.Stat(resolved)
			if statErr != nil {
				return statErr
			}
			if !info.IsDir() {
				return errors.New("local path exists and is not a directory")
			}
			current = resolved
			continue
		}
		if !info.IsDir() {
			return errors.New("local path exists and is not a directory")
		}
		current = candidate
	}
	return nil
}

func (f *LocalFS) Mkdir(relative string) error {
	absolute, _, err := f.Resolve(relative, true)
	if err != nil {
		return err
	}
	return os.Mkdir(absolute, 0o700)
}
func (f *LocalFS) Rename(oldRelative, newRelative string) error {
	oldPath, _, err := f.Resolve(oldRelative, false)
	if err != nil {
		return err
	}
	newPath, _, err := f.Resolve(newRelative, true)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(newPath); err == nil {
		return errors.New("destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return os.Rename(oldPath, newPath)
}
func (f *LocalFS) Remove(relative string) error {
	absolute, clean, err := f.Resolve(relative, false)
	if err != nil {
		return err
	}
	if clean == "." {
		return errors.New("the local root cannot be removed")
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return os.Remove(absolute)
	}
	return os.Remove(absolute)
}
func (f *LocalFS) Chmod(relative string, mode uint32) error {
	absolute, _, err := f.Resolve(relative, false)
	if err != nil {
		return err
	}
	if mode > 0o7777 {
		return errors.New("invalid file mode")
	}
	return os.Chmod(absolute, os.FileMode(mode))
}
func (f *LocalFS) Open(relative string) (*os.File, os.FileInfo, error) {
	absolute, _, err := f.Resolve(relative, false)
	if err != nil {
		return nil, nil, err
	}
	file, err := os.Open(absolute)
	if err != nil {
		return nil, nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, nil, err
	}
	if !info.Mode().IsRegular() {
		file.Close()
		return nil, nil, errors.New("local file is not a regular file")
	}
	return file, info, nil
}
func (f *LocalFS) ReplaceTemp(tempPath, targetRelative string, overwrite bool) error {
	target, _, err := f.Resolve(targetRelative, true)
	if err != nil {
		return err
	}
	if info, statErr := os.Lstat(target); statErr == nil {
		if !overwrite {
			return errors.New("local destination already exists")
		}
		if info.IsDir() {
			return errors.New("cannot replace a directory with a file")
		}
		backup := target + ".dccnext-backup-" + time.Now().UTC().Format("20060102150405.000000000")
		if err := os.Rename(target, backup); err != nil {
			return fmt.Errorf("preserve existing destination: %w", err)
		}
		if err := os.Rename(tempPath, target); err != nil {
			_ = os.Rename(backup, target)
			return err
		}
		_ = os.Remove(backup)
		return nil
	} else if !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	return os.Rename(tempPath, target)
}
func pathClean(value string) string {
	if value == "" {
		return "."
	}
	clean := filepath.ToSlash(filepath.Clean(filepath.FromSlash(value)))
	if clean == "" {
		return "."
	}
	return clean
}
func joinLocal(dir, name string) string {
	if dir == "." || dir == "" {
		return name
	}
	return filepath.ToSlash(filepath.Join(filepath.FromSlash(dir), name))
}
func withinRoot(root, candidate string) bool {
	rel, err := filepath.Rel(root, candidate)
	if err != nil {
		return false
	}
	if rel == "." {
		return true
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	if runtime.GOOS == "windows" {
		return !strings.HasPrefix(rel, "..")
	}
	return true
}
