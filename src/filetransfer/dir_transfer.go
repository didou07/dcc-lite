package filetransfer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/didou07/dcc-lite/src/core"
)

func checkRemoteDestination(ctx context.Context, adapter core.FileTransfer, remotePath string, isDirectory, overwrite bool) error {
	if remotePath == "/" || path.Base(remotePath) == "." || path.Base(remotePath) == "/" {
		return errors.New("a transfer destination must be a file or folder inside the receiver")
	}
	entries, err := adapter.List(ctx, path.Dir(remotePath))
	if err != nil {
		return fmt.Errorf("check remote destination: %w", err)
	}
	for _, entry := range entries {
		if entry.Name != path.Base(remotePath) {
			continue
		}
		if entry.IsDir != isDirectory {
			return errors.New("remote destination exists with a different file type")
		}
		if !overwrite {
			return errors.New("remote destination already exists; confirm overwrite to continue")
		}
		return nil
	}
	return nil
}

func uploadDirectory(ctx context.Context, adapter core.FileTransfer, localRoot, remoteRoot string, overwrite bool, progress func(core.TransferProgress)) error {
	type item struct {
		path string
		rel  string
		dir  bool
		size int64
	}
	items := make([]item, 0, 64)
	var total int64
	err := filepath.WalkDir(localRoot, func(current string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if current == localRoot {
			items = append(items, item{path: current, dir: true})
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return fmt.Errorf("folder transfer does not follow symbolic links: %s", entry.Name())
		}
		if invalidFTPArgument(entry.Name()) || strings.ContainsAny(entry.Name(), "/\\") {
			return fmt.Errorf("folder transfer found an unsupported file name: %q", entry.Name())
		}
		rel, err := filepath.Rel(localRoot, current)
		if err != nil {
			return err
		}
		if strings.Count(filepath.ToSlash(rel), "/")+1 > maxDirectoryDepth {
			return errors.New("folder transfer exceeds the maximum directory depth")
		}
		if len(items) >= maxDirectoryEntries {
			return errors.New("folder transfer exceeds the maximum entry count")
		}
		if entry.IsDir() {
			items = append(items, item{path: current, rel: filepath.ToSlash(rel), dir: true})
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported local file type: %s", entry.Name())
		}
		if info.Size() > 0 && total > int64(^uint64(0)>>1)-info.Size() {
			return errors.New("folder transfer size overflow")
		}
		total += info.Size()
		items = append(items, item{path: current, rel: filepath.ToSlash(rel), size: info.Size()})
		return nil
	})
	if err != nil {
		return err
	}
	if err := ensureRemoteDirectory(ctx, adapter, remoteRoot, overwrite); err != nil {
		return fmt.Errorf("create remote folder: %w", err)
	}
	var completed int64
	for _, item := range items {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		remote := remoteRoot
		if item.rel != "" {
			remote = path.Join(remoteRoot, item.rel)
		}
		if item.dir {
			if item.rel != "" {
				if err := ensureRemoteDirectory(ctx, adapter, remote, overwrite); err != nil {
					return err
				}
			}
			continue
		}
		err := adapter.Upload(ctx, item.path, remote, func(p core.TransferProgress) {
			if progress != nil {
				progress(core.TransferProgress{BytesDone: completed + p.BytesDone, BytesTotal: total, Fraction: fraction(completed+p.BytesDone, total)})
			}
		})
		if err != nil {
			return err
		}
		completed += item.size
	}
	if progress != nil {
		progress(core.TransferProgress{BytesDone: total, BytesTotal: total, Fraction: 1})
	}
	return nil
}

func ensureRemoteDirectory(ctx context.Context, adapter core.FileTransfer, remotePath string, overwrite bool) error {
	if err := adapter.Mkdir(ctx, remotePath); err == nil {
		return nil
	} else if !overwrite {
		return err
	}
	entries, listErr := adapter.List(ctx, path.Dir(remotePath))
	if listErr != nil {
		return listErr
	}
	for _, entry := range entries {
		if entry.Name == path.Base(remotePath) && entry.IsDir {
			return nil
		}
	}
	return errors.New("remote folder exists or could not be created")
}

type remoteItem struct {
	entry core.FileEntry
	local string
}

func downloadDirectory(ctx context.Context, adapter core.FileTransfer, local *LocalFS, remoteRoot, localRoot string, overwrite bool, progress func(core.TransferProgress)) error {
	if err := local.EnsureDir(localRoot); err != nil {
		return err
	}
	files := make([]remoteItem, 0, 64)
	var directories []string
	var total int64
	entryCount := 0
	var walk func(string, string, int) error
	walk = func(remoteDir, localDir string, depth int) error {
		if depth > maxDirectoryDepth {
			return errors.New("remote folder exceeds the maximum directory depth")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		entries, err := adapter.List(ctx, remoteDir)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			entryCount++
			if entryCount > maxDirectoryEntries {
				return errors.New("remote folder exceeds the maximum entry count")
			}
			if invalidFTPArgument(entry.Name) || entry.Name == "." || entry.Name == ".." || path.Base(entry.Name) != entry.Name || strings.ContainsAny(entry.Name, "/\\") {
				return fmt.Errorf("remote directory contains an unsafe entry name: %q", entry.Name)
			}
			if strings.HasPrefix(entry.Mode, "l") || strings.Contains(strings.ToLower(entry.Mode), "slink") {
				return fmt.Errorf("folder download does not follow remote symbolic links: %s", entry.Name)
			}
			localPath := joinLocal(localDir, entry.Name)
			remoteEntryPath := path.Join(remoteDir, entry.Name)
			if entry.IsDir {
				directories = append(directories, localPath)
				if err := walk(remoteEntryPath, localPath, depth+1); err != nil {
					return err
				}
				continue
			}
			if entry.Size > 0 && total > int64(^uint64(0)>>1)-entry.Size {
				return errors.New("remote folder size overflow")
			}
			entry.Path = remoteEntryPath
			files = append(files, remoteItem{entry: entry, local: localPath})
			if entry.Size > 0 {
				total += entry.Size
			}
		}
		return nil
	}
	if err := walk(remoteRoot, localRoot, 0); err != nil {
		return err
	}
	for _, dir := range directories {
		if err := local.EnsureDir(dir); err != nil {
			return err
		}
	}
	var completed int64
	for _, file := range files {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := downloadFile(ctx, adapter, local, file.entry.Path, file.local, overwrite, func(p core.TransferProgress) {
			if progress != nil {
				progress(core.TransferProgress{BytesDone: completed + p.BytesDone, BytesTotal: total, Fraction: fraction(completed+p.BytesDone, total)})
			}
		})
		if err != nil {
			return err
		}
		completed += file.entry.Size
	}
	if progress != nil {
		progress(core.TransferProgress{BytesDone: total, BytesTotal: total, Fraction: 1})
	}
	return nil
}

func downloadFile(ctx context.Context, adapter core.FileTransfer, local *LocalFS, remotePath, localRelative string, overwrite bool, progress func(core.TransferProgress)) error {
	localAbsolute, _, err := local.Resolve(localRelative, true)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(localAbsolute), ".dcclite-transfer-*.part")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	_ = tmp.Close()
	_ = os.Remove(tmpPath)
	if err := adapter.Download(ctx, remotePath, tmpPath, progress); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := local.ReplaceTemp(tmpPath, localRelative, overwrite); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	return nil
}
