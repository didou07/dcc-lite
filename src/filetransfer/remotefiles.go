package filetransfer

import (
	"context"
	"errors"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/didou07/dcc-lite/src/core"
)

// RemoteFile is one small file read from a receiver folder.
type RemoteFile struct {
	Name string
	Data []byte
}

// ErrTooMuchData is returned when a folder holds more than the caller allows.
var ErrTooMuchData = errors.New("the folder holds more data than a backup can contain")

// ReadDirFiles reads the regular files of one remote folder (not sub folders,
// links or hidden files) that keep() accepts. Files above maxFile bytes are
// reported in skipped instead of being read; maxTotal bounds the memory used.
func (m *Manager) ReadDirFiles(ctx context.Context, sessionID, dir string, keep func(name string) bool, maxFile, maxTotal int64) (files []RemoteFile, skipped []string, err error) {
	err = m.withAdapter(sessionID, func(adapter core.FileTransfer) error {
		entries, listErr := adapter.List(ctx, dir)
		if listErr != nil {
			return listErr
		}
		var total int64
		for _, entry := range entries {
			if entry.IsDir || strings.HasPrefix(entry.Mode, "l") || strings.HasPrefix(entry.Name, ".") || !keep(entry.Name) {
				continue
			}
			if entry.Size > maxFile {
				skipped = append(skipped, entry.Name)
				continue
			}
			data, readErr := downloadSmall(ctx, adapter, path.Join(dir, entry.Name), maxFile)
			if readErr != nil {
				return readErr
			}
			total += int64(len(data))
			if total > maxTotal {
				return ErrTooMuchData
			}
			files = append(files, RemoteFile{Name: entry.Name, Data: data})
		}
		return nil
	})
	return files, skipped, err
}

// PutFile writes data to a remote path without ever leaving a half written
// file: it uploads next to the target, then swaps it in (keeping the old
// permissions), the same way file transfers commit.
func (m *Manager) PutFile(ctx context.Context, sessionID, remotePath string, data []byte) error {
	return m.withAdapter(sessionID, func(adapter core.FileTransfer) error {
		id, err := newFileID()
		if err != nil {
			return err
		}
		tmpDir, err := os.MkdirTemp("", "dcclite-put-*")
		if err != nil {
			return err
		}
		defer os.RemoveAll(tmpDir)
		local := filepath.Join(tmpDir, "data")
		if err := os.WriteFile(local, data, 0o600); err != nil {
			return err
		}
		partial := partialRemotePath(remotePath, id)
		if err := adapter.Upload(ctx, local, partial, nil); err != nil {
			_ = adapter.Remove(ctx, partial, false)
			return err
		}
		if err := commitRemoteFile(ctx, adapter, partial, remotePath, true, id); err != nil {
			_ = adapter.Remove(ctx, partial, false)
			return err
		}
		return nil
	})
}
