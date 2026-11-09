package filetransfer

import (
	"context"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

const (
	ftpKeepAliveEvery = 25 * time.Second
	ftpProbeAfter     = 10 * time.Second
)

type ResilientFTP struct {
	options FTPOptions

	opMu sync.Mutex

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
