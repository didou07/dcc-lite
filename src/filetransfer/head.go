package filetransfer

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

// MaxHexBytes is how much of a file the hex viewer loads. Bigger files show their first part only.
const MaxHexBytes = 16 << 20

// BinaryFile is a file opened for the hex editor: its bytes (all of them unless Truncated) and what
// is needed to notice a change made by someone else before saving.
type BinaryFile struct {
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at"`
	Mode       string    `json:"mode"`
	Hash       string    `json:"hash,omitempty"` // of the whole file; empty when only the start was read
	Link       string    `json:"link,omitempty"`
	Truncated  bool      `json:"truncated"`
	Data       []byte    `json:"-"`
}

func newBinaryFile(path string, size int64, modified time.Time, mode, link string, data []byte) BinaryFile {
	file := BinaryFile{Path: path, Size: size, ModifiedAt: modified, Mode: mode, Link: link, Truncated: int64(len(data)) < size, Data: data}
	if !file.Truncated {
		file.Hash = hashBytes(data)
	}
	return file
}

// ReadHead returns up to limit bytes from the start of a remote file, with the file's full size.
// A file that fits is downloaded whole. For a bigger one the download is started and cut off once
// enough bytes have arrived, so a multi-gigabyte recording is never fetched in full.
func (m *Manager) ReadHead(ctx context.Context, sessionID, remotePath string, limit int64) (result BinaryFile, found bool, err error) {
	err = m.withAdapter(sessionID, func(adapter core.FileTransfer) error {
		entry, ok, listErr := findRemoteEntry(ctx, adapter, remotePath)
		if listErr != nil {
			return listErr
		}
		if !ok {
			return nil
		}
		if entry.IsDir {
			return ErrEditDirectory
		}
		found = true
		if entry.Size <= limit {
			data := []byte{}
			if entry.Size > 0 {
				var readErr error
				if data, readErr = downloadSmall(ctx, adapter, remotePath, limit); readErr != nil {
					return readErr
				}
			}
			result = newBinaryFile(remotePath, int64(len(data)), entry.ModifiedAt, entry.Mode, entry.LinkTarget, data)
			return nil
		}
		resumable, ok := adapter.(core.ResumableFileTransfer)
		if !ok {
			return ErrEditTooLarge
		}
		dir, mkErr := os.MkdirTemp("", "dcclite-head-*")
		if mkErr != nil {
			return mkErr
		}
		defer os.RemoveAll(dir)
		partial := filepath.Join(dir, "head")
		cut, cancel := context.WithCancel(ctx)
		defer cancel()
		dlErr := resumable.DownloadPartial(cut, remotePath, partial, func(p core.TransferProgress) {
			if p.BytesDone >= limit {
				cancel()
			}
		})
		head, readErr := readPrefix(partial, limit)
		if readErr != nil && !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		if int64(len(head)) < limit {
			if dlErr != nil {
				return fmt.Errorf("read the start of the file: %w", dlErr)
			}
			return errors.New("the receiver sent less of the file than expected")
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		result = newBinaryFile(remotePath, entry.Size, entry.ModifiedAt, entry.Mode, entry.LinkTarget, head)
		return nil
	})
	return result, found, err
}

// BinaryWrite is a save from the hex editor.
type BinaryWrite struct {
	ExpectedSize       int64
	ExpectedModifiedAt time.Time
	ExpectedHash       string
	Force              bool
	// Before gets the content now on the receiver and can stop the save, for example when a copy of
	// the original cannot be kept.
	Before func(current []byte) error
}

// WriteBytes replaces a file with the given bytes. Only a file that was read in full can be saved: a
// file bigger than MaxHexBytes is refused, so a cut-off copy can never overwrite the real one.
func (m *Manager) WriteBytes(ctx context.Context, sessionID, remotePath string, data []byte, write BinaryWrite) (BinaryFile, error) {
	if int64(len(data)) > MaxHexBytes {
		return BinaryFile{}, ErrEditTooLarge
	}
	var result BinaryFile
	err := m.withAdapter(sessionID, func(adapter core.FileTransfer) error {
		if entry, found, err := findRemoteEntry(ctx, adapter, remotePath); err != nil {
			return err
		} else if found && !entry.IsDir && entry.Size > MaxHexBytes {
			return ErrEditTooLarge
		}
		written, err := writeRemoteBytes(ctx, adapter, remotePath, data, remoteWrite{
			Force: write.Force, Limit: MaxHexBytes, Before: write.Before,
			ExpectedSize: write.ExpectedSize, ExpectedModifiedAt: write.ExpectedModifiedAt, ExpectedHash: write.ExpectedHash,
		})
		if err != nil {
			return err
		}
		result = BinaryFile{Path: remotePath, Size: written.Size, ModifiedAt: written.ModifiedAt, Mode: written.Mode, Hash: hashBytes(data), Link: written.Link}
		return nil
	})
	return result, err
}

func readPrefix(path string, limit int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return io.ReadAll(io.LimitReader(file, limit))
}

// ReadHead returns up to limit bytes from the start of a file on this computer.
func (f *LocalFS) ReadHead(relative string, limit int64) (BinaryFile, error) {
	absolute, clean, err := f.Resolve(relative, false)
	if err != nil {
		return BinaryFile{}, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return BinaryFile{}, ErrEditNotFound
		}
		return BinaryFile{}, err
	}
	if info.IsDir() {
		return BinaryFile{}, ErrEditDirectory
	}
	data, err := readPrefix(absolute, limit)
	if err != nil {
		return BinaryFile{}, err
	}
	return newBinaryFile(clean, info.Size(), info.ModTime().UTC(), info.Mode().String(), "", data), nil
}

// WriteBytes replaces a file on this computer with the given bytes, through a temporary file so an
// interruption cannot leave it half written.
func (f *LocalFS) WriteBytes(relative string, data []byte, write BinaryWrite) (BinaryFile, error) {
	if int64(len(data)) > MaxHexBytes {
		return BinaryFile{}, ErrEditTooLarge
	}
	absolute, clean, err := f.Resolve(relative, false)
	if err != nil {
		return BinaryFile{}, err
	}
	info, err := os.Lstat(absolute)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return BinaryFile{}, ErrEditNotFound
	case err != nil:
		return BinaryFile{}, err
	case info.IsDir():
		return BinaryFile{}, ErrEditDirectory
	case info.Size() > MaxHexBytes:
		return BinaryFile{}, ErrEditTooLarge
	case !write.Force && !sameLocalVersion(info, write.ExpectedSize, write.ExpectedModifiedAt):
		return BinaryFile{}, ErrEditConflict
	}
	current, err := os.ReadFile(absolute)
	if err != nil {
		return BinaryFile{}, err
	}
	if !write.Force && write.ExpectedHash != "" && hashBytes(current) != write.ExpectedHash {
		return BinaryFile{}, ErrEditConflict
	}
	if write.Before != nil {
		if err := write.Before(current); err != nil {
			return BinaryFile{}, err
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(absolute), ".dcclite-edit-*.tmp")
	if err != nil {
		return BinaryFile{}, err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return BinaryFile{}, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return BinaryFile{}, err
	}
	_ = os.Chmod(tmpPath, info.Mode().Perm())
	if err := os.Rename(tmpPath, absolute); err != nil {
		os.Remove(tmpPath)
		return BinaryFile{}, err
	}
	result := BinaryFile{Path: clean, Size: int64(len(data)), Hash: hashBytes(data)}
	if updated, err := os.Lstat(absolute); err == nil {
		result.ModifiedAt, result.Mode = updated.ModTime().UTC(), updated.Mode().String()
	}
	return result, nil
}
