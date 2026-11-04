package filetransfer

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"sync"

	"github.com/didou07/dcc-lite/src/core"
)

// MemoryFS is an in-memory core.FileTransfer used by API tests and demos. It
// follows the rules of the real adapters: listings report permissions, and a
// download never overwrites an existing local file.
type MemoryFS struct {
	mu    sync.Mutex
	files map[string][]byte
	modes map[string]uint32
	dirs  map[string]bool
}

// NewMemoryFS returns an empty file system containing only "/".
func NewMemoryFS() *MemoryFS {
	return &MemoryFS{files: map[string][]byte{}, modes: map[string]uint32{}, dirs: map[string]bool{"/": true}}
}

// Set creates or replaces a file (and its parent folders).
func (m *MemoryFS) Set(name, content string, mode uint32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mkdirAllLocked(path.Dir(name))
	m.files[name] = []byte(content)
	m.modes[name] = mode
}

// Get returns a file's content.
func (m *MemoryFS) Get(name string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[name]
	return string(data), ok
}

// Names lists every file path, sorted.
func (m *MemoryFS) Names() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.files))
	for name := range m.files {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (m *MemoryFS) mkdirAllLocked(dir string) {
	for dir != "/" && dir != "." && dir != "" {
		m.dirs[dir] = true
		dir = path.Dir(dir)
	}
}

func (m *MemoryFS) List(_ context.Context, dir string) ([]core.FileEntry, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.dirs[dir] {
		return nil, errors.New("remote directory not found")
	}
	seen := map[string]core.FileEntry{}
	for name := range m.dirs {
		if name != "/" && path.Dir(name) == dir {
			seen[path.Base(name)] = core.FileEntry{Name: path.Base(name), Path: name, IsDir: true}
		}
	}
	for name, data := range m.files {
		if path.Dir(name) == dir {
			seen[path.Base(name)] = core.FileEntry{Name: path.Base(name), Path: name, Size: int64(len(data)), Mode: fmt.Sprintf("%04o", m.modes[name])}
		}
	}
	out := make([]core.FileEntry, 0, len(seen))
	for _, entry := range seen {
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (m *MemoryFS) Upload(ctx context.Context, local, remote string, _ func(core.TransferProgress)) error {
	data, err := os.ReadFile(local)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mkdirAllLocked(path.Dir(remote))
	m.files[remote] = data
	if _, ok := m.modes[remote]; !ok {
		m.modes[remote] = 0o644
	}
	return nil
}

func (m *MemoryFS) Download(ctx context.Context, remote, local string, _ func(core.TransferProgress)) error {
	m.mu.Lock()
	data, ok := m.files[remote]
	data = append([]byte(nil), data...)
	m.mu.Unlock()
	if !ok {
		return errors.New("remote file not found")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if _, err := os.Stat(local); err == nil {
		return errors.New("local destination already exists")
	}
	return os.WriteFile(local, data, 0o600)
}

func (m *MemoryFS) Mkdir(_ context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mkdirAllLocked(name)
	return nil
}

func (m *MemoryFS) Rename(_ context.Context, from, to string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.files[from]
	if !ok {
		return errors.New("remote file not found")
	}
	if _, exists := m.files[to]; exists {
		return errors.New("destination exists")
	}
	m.files[to], m.modes[to] = data, m.modes[from]
	delete(m.files, from)
	delete(m.modes, from)
	return nil
}

func (m *MemoryFS) Remove(_ context.Context, name string, directory bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if directory {
		for file := range m.files {
			if strings.HasPrefix(file, name+"/") {
				return errors.New("directory not empty")
			}
		}
		delete(m.dirs, name)
		return nil
	}
	if _, ok := m.files[name]; !ok {
		return errors.New("remote file not found")
	}
	delete(m.files, name)
	delete(m.modes, name)
	return nil
}

func (m *MemoryFS) Chmod(_ context.Context, name string, mode uint32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.files[name]; !ok {
		return errors.New("remote file not found")
	}
	m.modes[name] = mode
	return nil
}

func (m *MemoryFS) Close() error { return nil }

// AttachAdapter registers an already-connected adapter as a file session. It
// exists for API tests and demos; real sessions are created with Open.
func (m *Manager) AttachAdapter(info SessionInfo, adapter core.FileTransfer) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[info.ID] = &managedSession{info: info, adapter: adapter}
}
