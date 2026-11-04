package filetransfer

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/didou07/dcc-lite/src/core"
)

// MaxEditBytes is the largest remote file the in-browser editor will open or save.
// Configuration files on a receiver are tiny; the limit keeps the editor from
// pulling large binaries or logs into the browser.
const MaxEditBytes = 1 << 20

var (
	ErrEditNotText   = errors.New("this file does not look like UTF-8 text, so it cannot be edited here")
	ErrEditTooLarge  = fmt.Errorf("this file is larger than the %d KiB edit limit; download it instead", MaxEditBytes/1024)
	ErrEditConflict  = errors.New("the remote file changed after it was opened; reload it or save with overwrite")
	ErrEditNotFound  = errors.New("the remote file no longer exists")
	ErrEditExists    = errors.New("a remote file with this name already exists")
	ErrEditDirectory = errors.New("a folder cannot be edited as text")
)

// Line ending names reported to and accepted from the editor.
const (
	LineEndingLF    = "lf"
	LineEndingCRLF  = "crlf"
	LineEndingMixed = "mixed"
)

// TextFile is the editable view of a remote file. Content always uses "\n"
// line breaks; LineEnding records what the file used so a save can restore it.
type TextFile struct {
	Path       string    `json:"path"`
	Content    string    `json:"content"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at,omitempty"`
	Mode       string    `json:"mode,omitempty"`
	LineEnding string    `json:"line_ending"`
	// Hash is the SHA-256 of the file as stored on the device. Saves send it
	// back so a change made meanwhile is detected even when the listing's size
	// and one-second timestamp did not move.
	Hash string `json:"hash"`
	// ModeUnknown is set after a save when the server's listing did not report
	// the file's permissions, so they could not be carried over to the new file.
	ModeUnknown bool `json:"mode_unknown,omitempty"`
}

// TextWrite describes a save request from the editor.
type TextWrite struct {
	Content    string
	LineEnding string
	// ExpectedSize and ExpectedModifiedAt come from the TextFile the editor
	// opened. They detect a remote change made while the editor was open.
	ExpectedSize       int64
	ExpectedModifiedAt time.Time
	// ExpectedHash is TextFile.Hash from when the editor opened the file.
	ExpectedHash string
	// Force skips the change check (the user chose to overwrite).
	Force bool
	// Create makes a new file and fails when the name already exists.
	Create bool
}

// ReadText downloads a small remote text file for editing.
func (m *Manager) ReadText(ctx context.Context, sessionID, remotePath string) (TextFile, error) {
	var result TextFile
	err := m.withAdapter(sessionID, func(adapter core.FileTransfer) error {
		entry, found, err := findRemoteEntry(ctx, adapter, remotePath)
		if err != nil {
			return err
		}
		if !found {
			return ErrEditNotFound
		}
		if entry.IsDir {
			return ErrEditDirectory
		}
		if entry.Size > MaxEditBytes {
			return ErrEditTooLarge
		}
		data, err := downloadSmall(ctx, adapter, remotePath, MaxEditBytes)
		if err != nil {
			return err
		}
		content, lineEnding, err := decodeText(data)
		if err != nil {
			return err
		}
		result = TextFile{
			Path: remotePath, Content: content, Size: entry.Size,
			ModifiedAt: entry.ModifiedAt, Mode: entry.Mode, LineEnding: lineEnding, Hash: hashBytes(data),
		}
		return nil
	})
	return result, err
}

// WriteText saves edited text back to the receiver. The new content is uploaded
// to a hidden partial file and swapped in with commitRemoteFile, so a dropped
// connection never leaves a half-written configuration file, and the existing
// permissions are preserved.
func (m *Manager) WriteText(ctx context.Context, sessionID, remotePath string, write TextWrite) (TextFile, error) {
	data, err := encodeText(write.Content, write.LineEnding)
	if err != nil {
		return TextFile{}, err
	}
	if base := path.Base(remotePath); remotePath == "/" || base == "." || base == "/" {
		return TextFile{}, errors.New("a file path is required")
	}
	var result TextFile
	err = m.withAdapter(sessionID, func(adapter core.FileTransfer) error {
		entry, found, err := findRemoteEntry(ctx, adapter, remotePath)
		if err != nil {
			return err
		}
		switch {
		case write.Create && found:
			return ErrEditExists
		case !write.Create && !found:
			return ErrEditNotFound
		case found && entry.IsDir:
			return ErrEditDirectory
		case found && !write.Force && !sameRemoteVersion(entry, write.ExpectedSize, write.ExpectedModifiedAt):
			return ErrEditConflict
		}
		if found && !write.Force && write.ExpectedHash != "" {
			if entry.Size > MaxEditBytes {
				return ErrEditConflict
			}
			current, err := downloadSmall(ctx, adapter, remotePath, MaxEditBytes)
			if err != nil {
				return err
			}
			if hashBytes(current) != write.ExpectedHash {
				return ErrEditConflict
			}
		}
		tmp, err := os.CreateTemp("", "dccnext-edit-*.tmp")
		if err != nil {
			return err
		}
		tmpPath := tmp.Name()
		defer os.Remove(tmpPath)
		if _, err := tmp.Write(data); err != nil {
			_ = tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		id, err := newFileID()
		if err != nil {
			return err
		}
		partial := partialRemotePath(remotePath, "edit-"+id)
		if err := adapter.Upload(ctx, tmpPath, partial, nil); err != nil {
			_ = adapter.Remove(context.WithoutCancel(ctx), partial, false)
			return fmt.Errorf("upload edited file: %w", err)
		}
		if err := commitRemoteFile(ctx, adapter, partial, remotePath, true, "edit-"+id); err != nil {
			_ = adapter.Remove(context.WithoutCancel(ctx), partial, false)
			return err
		}
		updated, _, err := findRemoteEntry(ctx, adapter, remotePath)
		if err != nil {
			// The save itself succeeded; report what we know.
			updated = core.FileEntry{Path: remotePath, Size: int64(len(data))}
		}
		lineEnding := write.LineEnding
		if lineEnding != LineEndingCRLF {
			lineEnding = LineEndingLF
		}
		result = TextFile{
			Path: remotePath, Content: write.Content, Size: int64(len(data)),
			ModifiedAt: updated.ModifiedAt, Mode: updated.Mode, LineEnding: lineEnding, Hash: hashBytes(data),
		}
		if updated.Size > 0 {
			result.Size = updated.Size
		}
		result.ModeUnknown = found && strings.TrimSpace(entry.Mode) == ""
		return nil
	})
	return result, err
}

func hashBytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func findRemoteEntry(ctx context.Context, adapter core.FileTransfer, remotePath string) (core.FileEntry, bool, error) {
	entries, err := adapter.List(ctx, path.Dir(remotePath))
	if err != nil {
		return core.FileEntry{}, false, fmt.Errorf("look up remote file: %w", err)
	}
	name := path.Base(remotePath)
	for _, entry := range entries {
		if entry.Name == name {
			return entry, true, nil
		}
	}
	return core.FileEntry{}, false, nil
}

// sameRemoteVersion compares a listing entry with what the editor opened.
// Listings may lack a modification time (zero); then only the size is compared.
func sameRemoteVersion(entry core.FileEntry, size int64, modified time.Time) bool {
	if entry.Size != size {
		return false
	}
	if entry.ModifiedAt.IsZero() || modified.IsZero() {
		return true
	}
	return entry.ModifiedAt.Truncate(time.Second).Equal(modified.Truncate(time.Second))
}

func downloadSmall(ctx context.Context, adapter core.FileTransfer, remotePath string, limit int64) ([]byte, error) {
	// Adapters refuse to download onto an existing file, so give them a path
	// inside a private temporary directory that does not exist yet.
	dir, err := os.MkdirTemp("", "dccnext-edit-*")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	tmpPath := filepath.Join(dir, "download")
	if err := adapter.Download(ctx, remotePath, tmpPath, nil); err != nil {
		return nil, fmt.Errorf("download file for editing: %w", err)
	}
	file, err := os.Open(tmpPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrEditTooLarge
	}
	return data, nil
}

// decodeText validates that data is UTF-8 text and normalises line breaks to "\n".
func decodeText(data []byte) (string, string, error) {
	if bytes.IndexByte(data, 0) >= 0 || !utf8.Valid(data) {
		return "", "", ErrEditNotText
	}
	text := string(data)
	crlf := strings.Count(text, "\r\n")
	lf := strings.Count(text, "\n")
	lineEnding := LineEndingLF
	switch {
	case crlf > 0 && crlf == lf:
		lineEnding = LineEndingCRLF
	case crlf > 0:
		lineEnding = LineEndingMixed
	}
	if crlf > 0 {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	return text, lineEnding, nil
}

// encodeText validates editor content and applies the requested line ending.
func encodeText(content, lineEnding string) ([]byte, error) {
	if strings.IndexByte(content, 0) >= 0 || !utf8.ValidString(content) {
		return nil, ErrEditNotText
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if lineEnding == LineEndingCRLF {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}
	if len(content) > MaxEditBytes {
		return nil, ErrEditTooLarge
	}
	return []byte(content), nil
}

// MaxReadBytes is the largest file ReadBytes will fetch (a big lamedb5 is a few MB).
const MaxReadBytes = 24 << 20

// ReadBytes downloads a remote file into memory. found is false when the file
// does not exist, which is not an error for optional files.
func (m *Manager) ReadBytes(ctx context.Context, sessionID, remotePath string) (data []byte, found bool, err error) {
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
		if entry.Size > MaxReadBytes {
			return ErrEditTooLarge
		}
		found = true
		data, listErr = downloadSmall(ctx, adapter, remotePath, MaxReadBytes)
		return listErr
	})
	return data, found, err
}
