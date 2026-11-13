package config

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

const (
	maxStoreBytes = 2 << 20
	maxProfiles   = 200
)

type ProfileStore struct {
	mu       sync.RWMutex
	path     string
	profiles map[string]Profile
	remote   Remote
}

func NewProfileStore(dataDir, iniPath string) (*ProfileStore, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create profile directory: %w", err)
	}
	if os.PathSeparator != '\\' {
		if err := os.Chmod(dataDir, 0o700); err != nil {
			return nil, fmt.Errorf("secure profile directory: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(iniPath), 0o755); err != nil {
		return nil, fmt.Errorf("create settings directory: %w", err)
	}
	s := &ProfileStore{path: iniPath, profiles: make(map[string]Profile)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *ProfileStore) Path() string { return s.path }

func (s *ProfileStore) List() []Profile {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Name == out[j].Name {
			return out[i].ID < out[j].ID
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func (s *ProfileStore) Get(id string) (Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.profiles[id]
	return p, ok
}

func (s *ProfileStore) Save(p Profile) (Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p.ID == "" {
		if len(s.profiles) >= maxProfiles {
			return Profile{}, fmt.Errorf("profile limit reached (%d)", maxProfiles)
		}
		id, err := newID()
		if err != nil {
			return Profile{}, err
		}
		p.ID = id
	} else if _, ok := s.profiles[p.ID]; !ok {
		return Profile{}, errors.New("profile does not exist")
	}
	p.Name = trimSpace(p.Name)
	p.Host = trimSpace(p.Host)
	p.Username = trimSpace(p.Username)
	if err := p.Validate(); err != nil {
		return Profile{}, err
	}
	previous, existed := s.profiles[p.ID]
	s.profiles[p.ID] = p
	if err := s.persistLocked(); err != nil {
		if existed {
			s.profiles[p.ID] = previous
		} else {
			delete(s.profiles, p.ID)
		}
		return Profile{}, err
	}
	return p, nil
}

func (s *ProfileStore) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.profiles[id]; !ok {
		return os.ErrNotExist
	}
	previous := s.profiles[id]
	delete(s.profiles, id)
	if err := s.persistLocked(); err != nil {
		s.profiles[id] = previous
		return err
	}
	return nil
}

func readLimited(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if os.PathSeparator != '\\' {
		if err := os.Chmod(path, 0o600); err != nil {
			return nil, fmt.Errorf("secure settings file: %w", err)
		}
	}
	data, err := io.ReadAll(io.LimitReader(f, maxStoreBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", filepath.Base(path), err)
	}
	if len(data) > maxStoreBytes {
		return nil, fmt.Errorf("%s exceeds the size limit", filepath.Base(path))
	}
	return data, nil
}

func (s *ProfileStore) load() error {
	data, err := readLimited(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return s.persistLocked()
	}
	if err != nil {
		return err
	}
	return s.loadINI(data)
}

func (s *ProfileStore) loadINI(data []byte) error {
	doc, err := decodeINI(data)
	if err != nil {
		return fmt.Errorf("%s: %w", filepath.Base(s.path), err)
	}
	s.remote = doc.Remote
	for _, p := range doc.Profiles {
		p.Name = trimSpace(p.Name)
		p.Host = trimSpace(p.Host)
		if err := p.Validate(); err != nil {
			return fmt.Errorf("%s: device %q: %w", filepath.Base(s.path), p.Name, err)
		}
		if _, exists := s.profiles[p.ID]; exists {
			return fmt.Errorf("%s: duplicate device id %q", filepath.Base(s.path), p.ID)
		}
		s.profiles[p.ID] = p
	}
	return nil
}

func (s *ProfileStore) persistLocked() error {
	list := make([]Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].Name == list[j].Name {
			return list[i].ID < list[j].ID
		}
		return list[i].Name < list[j].Name
	})
	data := encodeINI(Document{Profiles: list, Remote: s.remote})
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".dcc-lite-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary settings file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temporary settings file: %w", err)
	}
	n, writeErr := tmp.Write(data)
	if writeErr != nil || n != len(data) {
		_ = tmp.Close()
		if writeErr == nil {
			writeErr = io.ErrShortWrite
		}
		return fmt.Errorf("write settings file: %w", writeErr)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync settings file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close settings file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace settings file: %w", err)
	}
	if os.PathSeparator != '\\' {
		dir, err := os.Open(filepath.Dir(s.path))
		if err != nil {
			return fmt.Errorf("open settings directory for sync: %w", err)
		}
		defer dir.Close()
		if err := dir.Sync(); err != nil {
			return fmt.Errorf("sync settings directory: %w", err)
		}
	}
	return nil
}

func newID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate profile id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func trimSpace(s string) string {
	return strings.TrimSpace(s)
}

func (s *ProfileStore) Remote() Remote {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.remote
}

func (s *ProfileStore) SaveRemote(r Remote) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.remote
	s.remote = r
	if err := s.persistLocked(); err != nil {
		s.remote = previous
		return err
	}
	return nil
}

func (s *ProfileStore) PasswordRecord() (salt, hash string, iterations int) {
	r := s.Remote()
	return r.PasswordSalt, r.PasswordHash, r.PasswordIterations
}

func (s *ProfileStore) SavePasswordRecord(salt, hash string, iterations int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	previous := s.remote
	s.remote.PasswordSalt = salt
	s.remote.PasswordHash = hash
	s.remote.PasswordIterations = iterations
	if err := s.persistLocked(); err != nil {
		s.remote = previous
		return err
	}
	return nil
}
