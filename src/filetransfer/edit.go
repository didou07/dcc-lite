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

// MaxEditBytes is the largest file the editor opens. Channel lists (lamedb) on receivers with many
// channels pass 1 MiB, so the limit is 8 MiB; bigger files are better downloaded.
const MaxEditBytes = 8 << 20

var (
	ErrEditNotText   = errors.New("this file does not look like UTF-8 text, so it cannot be edited here")
	ErrEditTooLarge  = fmt.Errorf("this file is larger than the %d MB edit limit; download it instead", MaxEditBytes>>20)
	ErrEditConflict  = errors.New("the remote file changed after it was opened; reload it or save with overwrite")
	ErrEditNotFound  = errors.New("the remote file no longer exists")
	ErrEditExists    = errors.New("a remote file with this name already exists")
	ErrEditDirectory = errors.New("a folder cannot be edited as text")
	ErrEditEncoding  = errors.New("the text contains characters that this file's original encoding (Latin-1) cannot store; remove them or use plain Latin letters")
)

const (
	EncodingUTF8   = "utf-8"
	EncodingLatin1 = "latin1" // used for files that are not valid UTF-8; every byte maps to one character, so nothing is lost
)

const (
	LineEndingLF    = "lf"
	LineEndingCRLF  = "crlf"
	LineEndingMixed = "mixed"
)

type TextFile struct {
	Path       string    `json:"path"`
	Content    string    `json:"content"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at,omitempty"`
	Mode       string    `json:"mode,omitempty"`
	LineEnding string    `json:"line_ending"`
	Encoding   string    `json:"encoding,omitempty"`

	Hash string `json:"hash"`

	// Mixed is set when the file used both CRLF and LF. LineEnding then holds the more common one,
	// which is what saving uses, so the few odd lines are the only ones that change.
	Mixed bool `json:"mixed_line_endings,omitempty"`

	ModeUnknown bool `json:"mode_unknown,omitempty"`

	// Link is set when the path is a symbolic link; the device follows it, so the text is the target's.
	Link string `json:"link,omitempty"`
}

type TextWrite struct {
	Content    string
	LineEnding string
	Encoding   string // "" or EncodingUTF8, or EncodingLatin1 for a file that was opened as such

	ExpectedSize       int64
	ExpectedModifiedAt time.Time

	ExpectedHash string

	Force bool

	Create bool
}

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
		content, lineEnding, encoding, err := decodeText(data)
		if err != nil {
			return err
		}
		lineEnding, mixed := settleLineEnding(data, lineEnding)
		result = TextFile{
			Path: remotePath, Content: content, Size: entry.Size,
			ModifiedAt: entry.ModifiedAt, Mode: entry.Mode, LineEnding: lineEnding, Mixed: mixed, Encoding: encoding, Hash: hashBytes(data), Link: entry.LinkTarget,
		}
		return nil
	})
	return result, err
}

func (m *Manager) WriteText(ctx context.Context, sessionID, remotePath string, write TextWrite) (TextFile, error) {
	data, err := encodeText(write.Content, write.LineEnding, write.Encoding)
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
		tmp, err := os.CreateTemp("", "dcclite-edit-*.tmp")
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
		if found && entry.LinkTarget != "" {
			// A symbolic link: write through it so the link itself survives (renaming over it would
			// replace the link with a plain file).
			// This write cannot go through a temporary file, so an interrupted upload could leave the
			// target cut short. Keep the current content and put it back if the upload fails.
			previous, readErr := downloadSmall(ctx, adapter, remotePath, MaxEditBytes)
			if readErr != nil {
				return fmt.Errorf("could not keep a copy of the file before writing through the link, so nothing was changed: %w", readErr)
			}
			if err := adapter.Upload(ctx, tmpPath, remotePath, nil); err != nil {
				if restoreErr := restoreThroughLink(context.WithoutCancel(ctx), adapter, remotePath, previous); restoreErr != nil {
					return fmt.Errorf("upload edited file through the link: %w (the original could not be put back either: %v)", err, restoreErr)
				}
				return fmt.Errorf("upload edited file through the link: %w (the original was put back)", err)
			}
			updated, _, ferr := findRemoteEntry(ctx, adapter, remotePath)
			if ferr != nil {
				updated = core.FileEntry{Path: remotePath, Size: int64(len(data))}
			}
			le := write.LineEnding
			if le != LineEndingCRLF {
				le = LineEndingLF
			}
			result = TextFile{Path: remotePath, Content: write.Content, Size: int64(len(data)), ModifiedAt: updated.ModifiedAt, Mode: updated.Mode, LineEnding: le, Encoding: normalizeEncoding(write.Encoding), Hash: hashBytes(data), Link: entry.LinkTarget}
			if updated.Size > 0 {
				result.Size = updated.Size
			}
			return nil
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

			updated = core.FileEntry{Path: remotePath, Size: int64(len(data))}
		}
		lineEnding := write.LineEnding
		if lineEnding != LineEndingCRLF {
			lineEnding = LineEndingLF
		}
		result = TextFile{
			Path: remotePath, Content: write.Content, Size: int64(len(data)),
			ModifiedAt: updated.ModifiedAt, Mode: updated.Mode, LineEnding: lineEnding, Encoding: normalizeEncoding(write.Encoding), Hash: hashBytes(data),
		}
		if updated.Size > 0 {
			result.Size = updated.Size
		}
		result.ModeUnknown = found && strings.TrimSpace(entry.Mode) == ""
		return nil
	})
	return result, err
}

// settleLineEnding turns "mixed" into the ending most lines use (LF on a tie) and says it was mixed.
// The editor cannot show a different ending per line, so saving writes this one everywhere.
func settleLineEnding(data []byte, lineEnding string) (string, bool) {
	if lineEnding != LineEndingMixed {
		return lineEnding, false
	}
	crlf := bytes.Count(data, []byte("\r\n"))
	lf := bytes.Count(data, []byte("\n")) - crlf
	if crlf > lf {
		return LineEndingCRLF, true
	}
	return LineEndingLF, true
}

func restoreThroughLink(ctx context.Context, adapter core.FileTransfer, remotePath string, content []byte) error {
	file, err := os.CreateTemp("", "dcclite-edit-undo-*.tmp")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return adapter.Upload(ctx, name, remotePath, nil)
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

	dir, err := os.MkdirTemp("", "dcclite-edit-*")
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

func normalizeEncoding(e string) string {
	if e == EncodingLatin1 {
		return EncodingLatin1
	}
	return EncodingUTF8
}

// decodeText turns file bytes into editable text. Valid UTF-8 is used as is.
// Other text (old Latin-1 or Windows code pages, common on receivers) is opened
// as Latin-1 so that saving writes back exactly the same bytes. Files with NUL
// bytes are binary and are refused.
func decodeText(data []byte) (text, lineEnding, encoding string, err error) {
	if bytes.IndexByte(data, 0) >= 0 {
		return "", "", "", ErrEditNotText
	}
	encoding = EncodingUTF8
	if utf8.Valid(data) {
		text = string(data)
	} else {
		encoding = EncodingLatin1
		runes := make([]rune, len(data))
		for i, b := range data {
			runes[i] = rune(b)
		}
		text = string(runes)
	}
	crlf := strings.Count(text, "\r\n")
	lf := strings.Count(text, "\n")
	lineEnding = LineEndingLF
	switch {
	case crlf > 0 && crlf == lf:
		lineEnding = LineEndingCRLF
	case crlf > 0:
		lineEnding = LineEndingMixed
	}
	if crlf > 0 {
		text = strings.ReplaceAll(text, "\r\n", "\n")
	}
	return text, lineEnding, encoding, nil
}

func encodeText(content, lineEnding, encoding string) ([]byte, error) {
	if strings.IndexByte(content, 0) >= 0 || !utf8.ValidString(content) {
		return nil, ErrEditNotText
	}
	content = strings.ReplaceAll(content, "\r\n", "\n")
	if lineEnding == LineEndingCRLF {
		content = strings.ReplaceAll(content, "\n", "\r\n")
	}
	var out []byte
	if encoding == EncodingLatin1 {
		out = make([]byte, 0, len(content))
		for _, r := range content {
			if r > 0xFF {
				return nil, ErrEditEncoding
			}
			out = append(out, byte(r))
		}
	} else {
		out = []byte(content)
	}
	if len(out) > MaxEditBytes {
		return nil, ErrEditTooLarge
	}
	return out, nil
}

const MaxReadBytes = 24 << 20

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
