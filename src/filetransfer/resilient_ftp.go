package filetransfer

import (
	"context"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

const (
	// ftpKeepAliveEvery is how often an idle session sends NOOP. Enigma2 FTP
	// daemons and many routers drop a control connection that stays silent for a
	// few minutes; this keeps it open.
	ftpKeepAliveEvery = 25 * time.Second
	// ftpProbeAfter is how long a session may sit unused before the next
	// operation first checks (with NOOP) that the connection is still alive.
	ftpProbeAfter = 10 * time.Second
)

// ResilientFTP is one logical FTP session that survives a dropped control
// connection. It keeps the connection alive while idle and, when the receiver
// closed it anyway (idle timeout, reboot, Wi-Fi drop), opens a new one before the
// next operation instead of failing it. Operations that are safe to repeat are
// retried once on a fresh connection.
type ResilientFTP struct {
	options FTPOptions

	// opMu serialises operations: an FTP control connection handles one at a time.
	opMu sync.Mutex

	// mu guards the fields below.
	mu       sync.Mutex
	client   *FTPClient
	lastUsed time.Time
	closed   bool
	stop     chan struct{}
}

var (
	_ core.FileTransfer          = (*ResilientFTP)(nil)
	_ core.ResumableFileTransfer = (*ResilientFTP)(nil)
	_ core.FileTimePreserver     = (*ResilientFTP)(nil)
)

// OpenResilientFTP connects like OpenFTP and starts the keep-alive loop.
func OpenResilientFTP(ctx context.Context, options FTPOptions) (*ResilientFTP, error) {
	client, err := OpenFTP(ctx, options)
	if err != nil {
		return nil, err
	}
	session := &ResilientFTP{options: options, client: client, lastUsed: time.Now(), stop: make(chan struct{})}
	go session.keepAlive()
	return session, nil
}

func (r *ResilientFTP) keepAlive() {
	ticker := time.NewTicker(ftpKeepAliveEvery)
	defer ticker.Stop()
	for {
		select {
		case <-r.stop:
			return
		case <-ticker.C:
			// A listing or transfer is running; the connection is clearly in use.
			if !r.opMu.TryLock() {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			_, _ = r.ensureLocked(ctx, true)
			cancel()
			r.opMu.Unlock()
		}
	}
}

func (r *ResilientFTP) touch() {
	r.mu.Lock()
	r.lastUsed = time.Now()
	r.mu.Unlock()
}

// ensureLocked returns a working client. The caller holds opMu. With force it
// always probes the connection; otherwise only when the session was idle.
func (r *ResilientFTP) ensureLocked(ctx context.Context, force bool) (*FTPClient, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil, ErrSessionNotFound
	}
	client := r.client
	idle := time.Since(r.lastUsed)
	r.mu.Unlock()

	if client != nil && !client.IsClosed() {
		if !force && idle < ftpProbeAfter {
			return client, nil
		}
		if err := client.Noop(ctx); err == nil {
			r.touch()
			return client, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return r.reconnectLocked(ctx)
}

// reconnectLocked replaces the control connection with a new one. The caller holds opMu.
func (r *ResilientFTP) reconnectLocked(ctx context.Context) (*FTPClient, error) {
	r.mu.Lock()
	old := r.client
	r.client = nil
	r.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
	fresh, err := OpenFTP(ctx, r.options)
	if err != nil {
		return nil, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		_ = fresh.Close()
		return nil, ErrSessionNotFound
	}
	r.client = fresh
	r.lastUsed = time.Now()
	r.mu.Unlock()
	return fresh, nil
}

// run executes one operation on a working connection. With retry set, an
// operation that failed because the connection died is repeated once on a new one.
func (r *ResilientFTP) run(ctx context.Context, retry bool, fn func(*FTPClient) error) error {
	r.opMu.Lock()
	defer r.opMu.Unlock()
	client, err := r.ensureLocked(ctx, false)
	if err != nil {
		return err
	}
	err = fn(client)
	if err != nil && retry && ctx.Err() == nil && client.IsClosed() {
		fresh, reconnectErr := r.reconnectLocked(ctx)
		if reconnectErr == nil {
			err = fn(fresh)
		}
	}
	r.touch()
	return err
}

func (r *ResilientFTP) List(ctx context.Context, remotePath string) ([]core.FileEntry, error) {
	var entries []core.FileEntry
	err := r.run(ctx, true, func(c *FTPClient) error {
		var listErr error
		entries, listErr = c.List(ctx, remotePath)
		return listErr
	})
	if err != nil {
		return nil, err
	}
	return entries, nil
}

func (r *ResilientFTP) Upload(ctx context.Context, localPath, remotePath string, progress func(core.TransferProgress)) error {
	return r.run(ctx, false, func(c *FTPClient) error { return c.Upload(ctx, localPath, remotePath, progress) })
}

func (r *ResilientFTP) Download(ctx context.Context, remotePath, localPath string, progress func(core.TransferProgress)) error {
	return r.run(ctx, true, func(c *FTPClient) error { return c.Download(ctx, remotePath, localPath, progress) })
}

func (r *ResilientFTP) Mkdir(ctx context.Context, remotePath string) error {
	return r.run(ctx, false, func(c *FTPClient) error { return c.Mkdir(ctx, remotePath) })
}

func (r *ResilientFTP) Rename(ctx context.Context, oldPath, newPath string) error {
	return r.run(ctx, false, func(c *FTPClient) error { return c.Rename(ctx, oldPath, newPath) })
}

func (r *ResilientFTP) Remove(ctx context.Context, remotePath string, directory bool) error {
	return r.run(ctx, false, func(c *FTPClient) error { return c.Remove(ctx, remotePath, directory) })
}

func (r *ResilientFTP) Chmod(ctx context.Context, remotePath string, mode uint32) error {
	return r.run(ctx, true, func(c *FTPClient) error { return c.Chmod(ctx, remotePath, mode) })
}

func (r *ResilientFTP) SetModTime(ctx context.Context, remotePath string, modified time.Time) error {
	return r.run(ctx, true, func(c *FTPClient) error { return c.SetModTime(ctx, remotePath, modified) })
}

func (r *ResilientFTP) DownloadPartial(ctx context.Context, remotePath, partialPath string, progress func(core.TransferProgress)) error {
	return r.run(ctx, true, func(c *FTPClient) error { return c.DownloadPartial(ctx, remotePath, partialPath, progress) })
}

func (r *ResilientFTP) ResumeDownload(ctx context.Context, remotePath, partialPath string, progress func(core.TransferProgress)) error {
	return r.run(ctx, true, func(c *FTPClient) error { return c.ResumeDownload(ctx, remotePath, partialPath, progress) })
}

func (r *ResilientFTP) ResumeUpload(ctx context.Context, localPath, remotePath string, progress func(core.TransferProgress)) error {
	return r.run(ctx, false, func(c *FTPClient) error { return c.ResumeUpload(ctx, localPath, remotePath, progress) })
}

// Close ends the session and its keep-alive loop. It does not wait for a running
// operation; the manager cancels those first.
func (r *ResilientFTP) Close() error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return nil
	}
	r.closed = true
	client := r.client
	r.client = nil
	close(r.stop)
	r.mu.Unlock()
	if client != nil {
		return client.Close()
	}
	return nil
}
