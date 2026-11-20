package filetransfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

const computerPath = "@computer"

// LocalFS is a view of this computer's files.
type LocalFS struct {
	root  string
	multi bool
}

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
	return &LocalFS{root: filepath.Clean(root), multi: true}, nil
}

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

func (f *LocalFS) Root() string { return f.root }

func (f *LocalFS) RootLabel() string { return "Home" }
func (f *LocalFS) List(relative string) ([]core.FileEntry, string, error) {
	if f.multi {
		if c, err := f.cleanRelativePath(relative); err == nil && c == computerPath {
			return f.driveEntries(), computerPath, nil
		}
	}
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
	if clean == computerPath {
		return "", "", errors.New("choose a drive first")
	}
	if f.isAbsolute(clean) {
		return f.resolveAbsolute(clean, allowMissing)
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
	if f.multi && relative == computerPath {
		return computerPath, nil
	}
	if strings.HasPrefix(relative, "/") || filepath.IsAbs(relative) || filepath.VolumeName(relative) != "" {
		if !f.multi || !f.isAbsolute(relative) {
			return "", errors.New("absolute local paths are not allowed")
		}
		clean := pathClean(relative)
		if runtime.GOOS == "windows" && filepath.VolumeName(filepath.FromSlash(clean)) == clean {
			clean += "/"
		}
		return clean, nil
	}
	clean := pathClean(relative)
	if f.multi && (clean == computerPath || strings.HasPrefix(clean, computerPath+"/")) {
		return "", errors.New("choose a drive first")
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("local path escapes the permitted home directory")
	}
	return clean, nil
}

func (f *LocalFS) EnsureDir(relative string) error {
	clean, err := f.cleanRelativePath(relative)
	if err != nil {
		return err
	}
	if clean == "." {
		return nil
	}
	current, rest, absolute := f.root, clean, f.isAbsolute(clean)
	if absolute {
		current, rest = splitAbsolute(clean)
	}
	for _, component := range strings.Split(rest, "/") {
		if component == "" || component == "." {
			continue
		}
		candidate := filepath.Join(current, filepath.FromSlash(component))
		if !absolute && !withinRoot(f.root, candidate) {
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
			if !absolute && !withinRoot(f.root, resolved) {
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
	clean, err := f.cleanRelativePath(relative)
	if err != nil {
		return err
	}
	if clean == "." || f.isRootPath(clean) {
		return errors.New("the local root cannot be removed")
	}
	parent, _, err := f.Resolve(path.Dir(clean), false)
	if err != nil {
		return err
	}
	target := filepath.Join(parent, filepath.Base(filepath.FromSlash(clean)))
	info, err := os.Lstat(target)
	if err != nil {
		return err
	}
	if info.IsDir() && info.Mode()&os.ModeSymlink == 0 {
		return os.RemoveAll(target)
	}
	return os.Remove(target)
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
		backup := target + ".dcclite-backup-" + time.Now().UTC().Format("20060102150405.000000000")
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

func (f *LocalFS) Copy(ctx context.Context, srcRelative, dstRelative string, overwrite bool) error {
	srcClean, err := f.cleanRelativePath(srcRelative)
	if err != nil {
		return err
	}
	dstClean, err := f.cleanRelativePath(dstRelative)
	if err != nil {
		return err
	}
	if srcClean == computerPath || dstClean == computerPath {
		return errors.New("choose a drive first")
	}
	srcLexical := f.lexicalPath(srcClean)
	srcInfo, err := os.Lstat(srcLexical)
	if err != nil {
		return err
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 {
		return errors.New("copying symbolic links is not supported")
	}
	dstLexical := f.lexicalPath(dstClean)
	if info, statErr := os.Lstat(dstLexical); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return errors.New("copy destination is a symbolic link")
	} else if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	srcAbsolute, srcClean, err := f.Resolve(srcRelative, false)
	if err != nil {
		return err
	}
	dstAbsolute, dstClean, err := f.Resolve(dstRelative, true)
	if err != nil {
		return err
	}
	if dstClean == "." || f.isRootPath(dstClean) {
		return errors.New("the local root cannot be a copy destination")
	}
	if filepath.Clean(srcAbsolute) == filepath.Clean(dstAbsolute) {
		return errors.New("source and destination are the same path")
	}
	if srcInfo.IsDir() {
		relative, relErr := filepath.Rel(srcAbsolute, dstAbsolute)
		if relErr != nil {
			return relErr
		}
		if relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return errors.New("cannot copy a folder into itself")
		}
	}
	dstInfo, statErr := os.Lstat(dstAbsolute)
	dstExists := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return statErr
	}
	if dstExists {
		if !overwrite {
			return errors.New("destination already exists")
		}
		if dstInfo.Mode()&os.ModeSymlink != 0 {
			return errors.New("copy destination is a symbolic link")
		}
		if dstInfo.IsDir() != srcInfo.IsDir() {
			return errors.New("destination exists with a different file type")
		}
	}
	if srcInfo.IsDir() {
		return copyLocalDirTransaction(ctx, srcAbsolute, dstAbsolute, srcInfo, dstExists)
	}
	return copyLocalFile(ctx, srcAbsolute, dstAbsolute, srcInfo, overwrite)
}

func (f *LocalFS) lexicalPath(clean string) string {
	if f.isAbsolute(clean) {
		return filepath.Clean(filepath.FromSlash(clean))
	}
	return filepath.Join(f.root, filepath.FromSlash(clean))
}

type contextReader struct {
	ctx context.Context
	r   io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.r.Read(buffer)
}

func copyLocalFile(ctx context.Context, src, dst string, sourceInfo os.FileInfo, overwrite bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	before, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if before.Mode()&os.ModeSymlink != 0 || !before.Mode().IsRegular() {
		return errors.New("copy source is not a regular file")
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	opened, err := in.Stat()
	if err != nil {
		return err
	}
	if !opened.Mode().IsRegular() || !os.SameFile(before, opened) || !os.SameFile(sourceInfo, opened) {
		return errors.New("copy source changed while it was being opened")
	}
	sourceSize, sourceModTime := opened.Size(), opened.ModTime()
	tmp, err := os.CreateTemp(filepath.Dir(dst), ".dcclite-copy-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_, copyErr := io.Copy(tmp, contextReader{ctx: ctx, r: in})
	closeErr := tmp.Close()
	if copyErr == nil {
		copyErr = closeErr
	}
	finalSource, statErr := in.Stat()
	if copyErr == nil && statErr != nil {
		copyErr = statErr
	}
	if copyErr == nil && (!os.SameFile(opened, finalSource) || finalSource.Size() != sourceSize || !finalSource.ModTime().Equal(sourceModTime)) {
		copyErr = errors.New("copy source changed while it was being copied")
	}
	if copyErr != nil {
		os.Remove(tmpPath)
		return copyErr
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, sourceInfo.Mode().Perm()); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if !sourceInfo.ModTime().IsZero() {
		if err := os.Chtimes(tmpPath, time.Now(), sourceInfo.ModTime()); err != nil {
			_ = os.Remove(tmpPath)
			return err
		}
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := commitLocalFile(tmpPath, dst, overwrite); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}

func reserveSiblingPath(target, prefix string) (string, error) {
	file, err := os.CreateTemp(filepath.Dir(target), prefix)
	if err != nil {
		return "", err
	}
	name := file.Name()
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return "", err
	}
	if err := os.Remove(name); err != nil {
		return "", err
	}
	return name, nil
}

func commitLocalFile(tempPath, target string, overwrite bool) error {
	info, err := os.Lstat(target)
	if errors.Is(err, os.ErrNotExist) {
		return os.Rename(tempPath, target)
	}
	if err != nil {
		return err
	}
	if !overwrite {
		return errors.New("destination already exists")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("copy destination is a symbolic link")
	}
	if info.IsDir() {
		return errors.New("destination exists with a different file type")
	}
	backup, err := reserveSiblingPath(target, ".dcclite-backup-")
	if err != nil {
		return fmt.Errorf("prepare destination backup: %w", err)
	}
	if err := os.Rename(target, backup); err != nil {
		return err
	}
	if err := os.Rename(tempPath, target); err != nil {
		if restoreErr := os.Rename(backup, target); restoreErr != nil {
			return fmt.Errorf("replace destination: %v; restore previous file: %w", err, restoreErr)
		}
		return err
	}
	if err := os.Remove(backup); err != nil {
		return fmt.Errorf("file copied but previous destination backup could not be removed: %w", err)
	}
	return nil
}

func copyLocalDirTransaction(ctx context.Context, src, dst string, sourceInfo os.FileInfo, overwrite bool) error {
	stage, err := os.MkdirTemp(filepath.Dir(dst), ".dcclite-copy-")
	if err != nil {
		return fmt.Errorf("create copy staging directory: %w", err)
	}
	defer os.RemoveAll(stage)
	entries := 0
	if overwrite {
		if err := copyLocalDir(ctx, dst, stage, true, 0, &entries); err != nil {
			return fmt.Errorf("preserve existing destination: %w", err)
		}
	}
	if err := copyLocalDir(ctx, src, stage, true, 0, &entries); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.Chmod(stage, sourceInfo.Mode().Perm()); err != nil {
		return fmt.Errorf("set copied directory permissions: %w", err)
	}
	if !sourceInfo.ModTime().IsZero() {
		if err := os.Chtimes(stage, time.Now(), sourceInfo.ModTime()); err != nil {
			return fmt.Errorf("set copied directory modification time: %w", err)
		}
	}
	if !overwrite {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := os.Rename(stage, dst); err != nil {
			return err
		}
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	backup, err := reserveSiblingPath(dst, ".dcclite-backup-")
	if err != nil {
		return fmt.Errorf("prepare destination backup: %w", err)
	}
	if err := os.Rename(dst, backup); err != nil {
		return err
	}
	if err := os.Rename(stage, dst); err != nil {
		if restoreErr := os.Rename(backup, dst); restoreErr != nil {
			return fmt.Errorf("replace destination: %v; restore previous directory: %w", err, restoreErr)
		}
		return err
	}
	if err := os.RemoveAll(backup); err != nil {
		return fmt.Errorf("directory copied but previous destination backup could not be removed: %w", err)
	}
	return nil
}

func copyLocalDir(ctx context.Context, src, dst string, overwrite bool, depth int, entries *int) error {
	if depth > maxDirectoryDepth {
		return errors.New("folder copy exceeds the maximum directory depth")
	}
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return err
	}
	if srcInfo.Mode()&os.ModeSymlink != 0 || !srcInfo.IsDir() {
		return errors.New("copy source is not a regular directory")
	}
	dstInfo, err := os.Lstat(dst)
	if errors.Is(err, os.ErrNotExist) {
		if err := os.Mkdir(dst, 0o700); err != nil {
			return err
		}
		dstInfo, err = os.Lstat(dst)
	}
	if err != nil {
		return err
	}
	if dstInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("copy destination contains a symbolic link: %s", filepath.Base(dst))
	}
	if !dstInfo.IsDir() {
		return fmt.Errorf("copy destination is not a directory: %s", filepath.Base(dst))
	}
	items, err := os.ReadDir(src)
	if err != nil {
		return err
	}
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return err
		}
		*entries++
		if *entries > maxDirectoryEntries {
			return errors.New("folder copy exceeds the maximum entry count")
		}
		info, err := item.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("folder copy does not follow symbolic links: %s", item.Name())
		}
		srcChild := filepath.Join(src, item.Name())
		dstChild := filepath.Join(dst, item.Name())
		if item.IsDir() {
			if existing, statErr := os.Lstat(dstChild); statErr == nil {
				if existing.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("copy destination contains a symbolic link: %s", item.Name())
				}
				if !existing.IsDir() {
					return fmt.Errorf("destination %q exists with a different file type", item.Name())
				}
			} else if !errors.Is(statErr, os.ErrNotExist) {
				return statErr
			}
			if err := copyLocalDir(ctx, srcChild, dstChild, overwrite, depth+1, entries); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported local file type: %s", item.Name())
		}
		if existing, statErr := os.Lstat(dstChild); statErr == nil {
			if existing.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("copy destination contains a symbolic link: %s", item.Name())
			}
			if !overwrite {
				return fmt.Errorf("destination already contains %q", item.Name())
			}
			if existing.IsDir() {
				return fmt.Errorf("destination %q exists with a different file type", item.Name())
			}
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return statErr
		}
		if err := copyLocalFile(ctx, srcChild, dstChild, info, overwrite); err != nil {
			return err
		}
	}
	if err := os.Chmod(dst, srcInfo.Mode().Perm()); err != nil {
		return err
	}
	if !srcInfo.ModTime().IsZero() {
		return os.Chtimes(dst, time.Now(), srcInfo.ModTime())
	}
	return nil
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

func (f *LocalFS) isAbsolute(value string) bool {
	if !f.multi || value == computerPath {
		return false
	}
	if runtime.GOOS == "windows" {
		return filepath.VolumeName(filepath.FromSlash(value)) != ""
	}
	return strings.HasPrefix(value, "/")
}

// isRootPath is true for a drive root ("D:/") or "/".
func (f *LocalFS) isRootPath(clean string) bool {
	if !f.isAbsolute(clean) {
		return false
	}
	base, rest := splitAbsolute(clean)
	return base != "" && strings.Trim(rest, "/") == ""
}

func splitAbsolute(clean string) (string, string) {
	if runtime.GOOS == "windows" {
		vol := filepath.VolumeName(filepath.FromSlash(clean))
		return vol + `\`, strings.TrimPrefix(clean[len(vol):], "/")
	}
	return "/", strings.TrimPrefix(clean, "/")
}

func (f *LocalFS) resolveAbsolute(clean string, allowMissing bool) (string, string, error) {
	candidate := filepath.FromSlash(clean)
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		if !allowMissing || !errors.Is(err, os.ErrNotExist) {
			return "", "", err
		}
		resolvedParent, parentErr := filepath.EvalSymlinks(filepath.Dir(candidate))
		if parentErr != nil {
			return "", "", parentErr
		}
		resolved = filepath.Join(resolvedParent, filepath.Base(candidate))
	}
	return resolved, clean, nil
}

func (f *LocalFS) ParentOf(current string) string {
	if !f.multi {
		return pathParent(current)
	}
	switch {
	case current == computerPath:
		return computerPath
	case current == "." || current == "":
		parent := filepath.Dir(f.root)
		if parent == f.root {
			return computerPath
		}
		return filepath.ToSlash(parent)
	case f.isAbsolute(current):
		if f.isRootPath(current) {
			return computerPath
		}
		parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(current)))
		if runtime.GOOS == "windows" && filepath.VolumeName(filepath.FromSlash(parent)) == parent {
			parent += "/"
		}
		return parent
	}
	return pathParent(current)
}

func pathParent(current string) string {
	if current == "." || current == "" {
		return "."
	}
	return filepath.ToSlash(filepath.Dir(filepath.FromSlash(current)))
}

func (f *LocalFS) driveEntries() []core.FileEntry {
	drives := listDrives()
	out := make([]core.FileEntry, 0, len(drives))
	for _, d := range drives {
		out = append(out, core.FileEntry{Name: d.Label, Path: d.Path, IsDir: true})
	}
	return out
}

type Place struct {
	Label string `json:"label"`
	Path  string `json:"path"`
	Kind  string `json:"kind"` // home, folder, computer, drive
}

// Places returns the quick locations.
func (f *LocalFS) Places() []Place {
	places := []Place{{Label: "Home", Path: ".", Kind: "home"}}
	for _, name := range []string{"Desktop", "Documents", "Downloads"} {
		if info, err := os.Stat(filepath.Join(f.root, name)); err == nil && info.IsDir() {
			places = append(places, Place{Label: name, Path: name, Kind: "folder"})
		}
	}
	places = append(places, Place{Label: "This computer", Path: computerPath, Kind: "computer"})
	for _, d := range listDrives() {
		places = append(places, Place{Label: d.Label, Path: d.Path, Kind: "drive"})
	}
	return places
}
