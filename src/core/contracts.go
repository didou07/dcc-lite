// Package core defines stable contracts shared by the file-transfer and terminal adapters.
// Protocol implementations live in separate packages so one transport cannot leak into another.
package core

import (
	"context"
	"errors"
	"io"
	"time"
)

var ErrMetadataUnsupported = errors.New("remote server does not support this metadata operation")

type FileEntry struct {
	Name       string    `json:"name"`
	Path       string    `json:"path"`
	Size       int64     `json:"size"`
	ModifiedAt time.Time `json:"modified_at,omitempty"`
	IsDir      bool      `json:"is_dir"`
	Mode       string    `json:"mode,omitempty"`
}

type TransferProgress struct {
	BytesDone           int64   `json:"bytes_done"`
	BytesTotal          int64   `json:"bytes_total,omitempty"`
	Fraction            float64 `json:"fraction,omitempty"`
	SpeedBytesPerSecond int64   `json:"speed_bytes_per_second,omitempty"`
	ETASeconds          int64   `json:"eta_seconds,omitempty"`
}

type FileTransfer interface {
	List(context.Context, string) ([]FileEntry, error)
	Upload(context.Context, string, string, func(TransferProgress)) error
	Download(context.Context, string, string, func(TransferProgress)) error
	Mkdir(context.Context, string) error
	Rename(context.Context, string, string) error
	Remove(context.Context, string, bool) error
	Chmod(context.Context, string, uint32) error
	Close() error
}

// ResumableFileTransfer is an optional capability implemented by protocol
// adapters that can preserve partial files and continue from a byte offset.
// Partial downloads are stored at an application-owned temporary path and
// committed to the requested destination only after the transfer completes.
type ResumableFileTransfer interface {
	DownloadPartial(context.Context, string, string, func(TransferProgress)) error
	ResumeDownload(context.Context, string, string, func(TransferProgress)) error
	ResumeUpload(context.Context, string, string, func(TransferProgress)) error
}

// FileTimePreserver is an optional protocol extension. Adapters
// normally preserve upload times through their native put/reput -p commands;
// FTP adapters may implement MFMT when the server supports the extension.
type FileTimePreserver interface {
	SetModTime(context.Context, string, time.Time) error
}

type TerminalSession interface {
	io.ReadWriteCloser
	Resize(columns, rows int) error
}
