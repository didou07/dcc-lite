package filetransfer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/didou07/dcc-lite/src/core"
)

func (m *Manager) List(ctx context.Context, sessionID, remotePath string) ([]core.FileEntry, error) {
	session, err := m.get(sessionID)
	if err != nil {
		return nil, err
	}
	session.mu.Lock()
	defer session.mu.Unlock()
	if session.closed {
		return nil, ErrSessionNotFound
	}
	entries, err := session.adapter.List(ctx, remotePath)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	return entries, nil
}

func (m *Manager) DownloadFile(ctx context.Context, sessionID, remotePath, localPath string) error {
	return m.withAdapter(sessionID, func(adapter core.FileTransfer) error {
		return adapter.Download(ctx, remotePath, localPath, nil)
	})
}

func (m *Manager) UploadFile(ctx context.Context, sessionID, localPath, remotePath string) error {
	return m.withAdapter(sessionID, func(adapter core.FileTransfer) error {
		return adapter.Upload(ctx, localPath, remotePath, nil)
	})
}

func (m *Manager) Mkdir(ctx context.Context, sessionID, remotePath string) error {
	return m.withAdapter(sessionID, func(adapter core.FileTransfer) error { return adapter.Mkdir(ctx, remotePath) })
}

func (m *Manager) Rename(ctx context.Context, sessionID, oldPath, newPath string) error {
	return m.withAdapter(sessionID, func(adapter core.FileTransfer) error { return adapter.Rename(ctx, oldPath, newPath) })
}

func (m *Manager) Remove(ctx context.Context, sessionID, remotePath string, directory bool) error {
	return m.withAdapter(sessionID, func(adapter core.FileTransfer) error {
		if !directory {
			return adapter.Remove(ctx, remotePath, false)
		}
		clean := path.Clean(remotePath)
		if clean == "/" || clean == "." || clean == "" {
			return errors.New("refusing to delete the root folder")
		}
		budget := maxDirectoryEntries
		return removeTree(ctx, adapter, clean, 0, &budget)
	})
}

func removeTree(ctx context.Context, adapter core.FileTransfer, dir string, depth int, budget *int) error {
	if depth > maxDirectoryDepth {
		return errors.New("folder is nested too deeply to delete")
	}
	entries, err := adapter.List(ctx, dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return err
		}
		if *budget--; *budget < 0 {
			return errors.New("folder has too many items to delete in one step")
		}
		if entry.Name == "" || entry.Name == "." || entry.Name == ".." {
			continue
		}
		child := path.Join(dir, entry.Name)
		isLink := strings.HasPrefix(entry.Mode, "l")
		if entry.IsDir && !isLink {
			if err := removeTree(ctx, adapter, child, depth+1, budget); err != nil {
				return err
			}
			continue
		}
		if err := adapter.Remove(ctx, child, false); err != nil {
			return fmt.Errorf("%s: %w", child, err)
		}
	}
	return adapter.Remove(ctx, dir, true)
}

func (m *Manager) Chmod(ctx context.Context, sessionID, remotePath string, mode uint32) error {
	return m.withAdapter(sessionID, func(adapter core.FileTransfer) error { return adapter.Chmod(ctx, remotePath, mode) })
}

func (m *Manager) Copy(ctx context.Context, sessionID, srcPath, dstPath string, overwrite bool) error {
	if err := validateRemotePath(srcPath); err != nil {
		return err
	}
	if err := validateRemotePath(dstPath); err != nil {
		return err
	}
	if dstPath == srcPath || strings.HasPrefix(dstPath, strings.TrimSuffix(srcPath, "/")+"/") {
		return errors.New("cannot copy a folder into itself")
	}
	return m.withAdapter(sessionID, func(adapter core.FileTransfer) error {
		entries, err := adapter.List(ctx, path.Dir(srcPath))
		if err != nil {
			return fmt.Errorf("look up source: %w", err)
		}
		var srcEntry core.FileEntry
		found := false
		for _, entry := range entries {
			if entry.Name == path.Base(srcPath) {
				srcEntry, found = entry, true
				break
			}
		}
		if !found {
			return errors.New("source no longer exists")
		}
		if strings.HasPrefix(srcEntry.Mode, "l") {
			return errors.New("copying symbolic links is not supported")
		}
		if err := checkRemoteDestination(ctx, adapter, dstPath, srcEntry.IsDir, overwrite); err != nil {
			return err
		}
		if srcEntry.IsDir {
			count := 0
			return copyRemoteDirectory(ctx, adapter, srcPath, dstPath, overwrite, 0, &count)
		}
		return copyRemoteFile(ctx, adapter, srcPath, dstPath, srcEntry.Mode)
	})
}

func copyRemoteFile(ctx context.Context, adapter core.FileTransfer, src, dst, mode string) error {
	dir, err := os.MkdirTemp("", "dcclite-copy-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	tmpPath := filepath.Join(dir, "copy")
	if err := adapter.Download(ctx, src, tmpPath, nil); err != nil {
		return fmt.Errorf("read source for copy: %w", err)
	}
	if err := adapter.Upload(ctx, tmpPath, dst, nil); err != nil {
		return err
	}
	if perm, ok := parseRemoteMode(mode); ok {
		_ = adapter.Chmod(ctx, dst, perm&0o7777) // best effort: some servers do not support CHMOD
	}
	return nil
}

func copyRemoteDirectory(ctx context.Context, adapter core.FileTransfer, srcRoot, dstRoot string, overwrite bool, depth int, count *int) error {
	if depth > maxDirectoryDepth {
		return errors.New("folder copy exceeds the maximum directory depth")
	}
	if err := ensureRemoteDirectory(ctx, adapter, dstRoot, overwrite); err != nil {
		return err
	}
	entries, err := adapter.List(ctx, srcRoot)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		*count++
		if *count > maxDirectoryEntries {
			return errors.New("folder copy exceeds the maximum entry count")
		}
		if invalidFTPArgument(entry.Name) || strings.ContainsAny(entry.Name, "/\\") {
			return fmt.Errorf("unsupported file name: %q", entry.Name)
		}
		if strings.HasPrefix(entry.Mode, "l") {
			return fmt.Errorf("folder copy does not follow symbolic links: %s", entry.Name)
		}
		srcChild := path.Join(srcRoot, entry.Name)
		dstChild := path.Join(dstRoot, entry.Name)
		if entry.IsDir {
			if err := copyRemoteDirectory(ctx, adapter, srcChild, dstChild, overwrite, depth+1, count); err != nil {
				return err
			}
			continue
		}
		if err := checkRemoteDestination(ctx, adapter, dstChild, false, overwrite); err != nil {
			return err
		}
		if err := copyRemoteFile(ctx, adapter, srcChild, dstChild, entry.Mode); err != nil {
			return err
		}
	}
	return nil
}

func RemoteParent(value string) string { return path.Dir(value) }
