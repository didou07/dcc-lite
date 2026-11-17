package filetransfer

import (
	"errors"
	"os"
	"path/filepath"
	"time"
)

func (f *LocalFS) ReadText(relative string) (TextFile, error) {
	absolute, clean, err := f.Resolve(relative, false)
	if err != nil {
		return TextFile{}, err
	}
	info, err := os.Lstat(absolute)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return TextFile{}, ErrEditNotFound
		}
		return TextFile{}, err
	}
	if info.IsDir() {
		return TextFile{}, ErrEditDirectory
	}
	if info.Size() > MaxEditBytes {
		return TextFile{}, ErrEditTooLarge
	}
	data, err := os.ReadFile(absolute)
	if err != nil {
		return TextFile{}, err
	}
	if int64(len(data)) > MaxEditBytes {
		return TextFile{}, ErrEditTooLarge
	}
	content, lineEnding, err := decodeText(data)
	if err != nil {
		return TextFile{}, err
	}
	return TextFile{
		Path: clean, Content: content, Size: info.Size(), ModifiedAt: info.ModTime().UTC(),
		Mode: info.Mode().String(), LineEnding: lineEnding, Hash: hashBytes(data),
	}, nil
}

func (f *LocalFS) WriteText(relative string, write TextWrite) (TextFile, error) {
	data, err := encodeText(write.Content, write.LineEnding)
	if err != nil {
		return TextFile{}, err
	}
	absolute, clean, err := f.Resolve(relative, true)
	if err != nil {
		return TextFile{}, err
	}
	if clean == "." {
		return TextFile{}, errors.New("a file path is required")
	}
	info, statErr := os.Lstat(absolute)
	found := statErr == nil
	if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
		return TextFile{}, statErr
	}
	switch {
	case write.Create && found:
		return TextFile{}, ErrEditExists
	case !write.Create && !found:
		return TextFile{}, ErrEditNotFound
	case found && info.IsDir():
		return TextFile{}, ErrEditDirectory
	case found && !write.Force && !sameLocalVersion(info, write.ExpectedSize, write.ExpectedModifiedAt):
		return TextFile{}, ErrEditConflict
	}
	if found && !write.Force && write.ExpectedHash != "" {
		if info.Size() > MaxEditBytes {
			return TextFile{}, ErrEditConflict
		}
		current, err := os.ReadFile(absolute)
		if err != nil {
			return TextFile{}, err
		}
		if hashBytes(current) != write.ExpectedHash {
			return TextFile{}, ErrEditConflict
		}
	}
	tmp, err := os.CreateTemp(filepath.Dir(absolute), ".dcclite-edit-*.tmp")
	if err != nil {
		return TextFile{}, err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpPath)
		return TextFile{}, err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpPath)
		return TextFile{}, err
	}
	if found {
		_ = os.Chmod(tmpPath, info.Mode().Perm())
	}
	if err := os.Rename(tmpPath, absolute); err != nil {
		os.Remove(tmpPath)
		return TextFile{}, err
	}
	lineEnding := write.LineEnding
	if lineEnding != LineEndingCRLF {
		lineEnding = LineEndingLF
	}
	result := TextFile{Path: clean, Content: write.Content, Size: int64(len(data)), LineEnding: lineEnding, Hash: hashBytes(data)}
	if updated, err := os.Lstat(absolute); err == nil {
		result.ModifiedAt = updated.ModTime().UTC()
		result.Mode = updated.Mode().String()
	}
	return result, nil
}

func sameLocalVersion(info os.FileInfo, size int64, modified time.Time) bool {
	if info.Size() != size {
		return false
	}
	if info.ModTime().IsZero() || modified.IsZero() {
		return true
	}
	return info.ModTime().Truncate(time.Second).Equal(modified.Truncate(time.Second))
}
