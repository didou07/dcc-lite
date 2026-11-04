// Package config stores the saved device profiles (and their passwords) on disk.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const (
	maxStoreBytes = 2 << 20
	maxProfiles   = 200
)

type profileRecord struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Host        string    `json:"host"`
	Port        int       `json:"port,omitempty"`
	Scheme      string    `json:"scheme,omitempty"`
	FTPPort     int       `json:"ftp_port,omitempty"`
	TelnetPort  int       `json:"telnet_port,omitempty"`
	Username    string    `json:"username,omitempty"`
	Password    string    `json:"password,omitempty"`
	Enabled     bool      `json:"enabled"`
	InsecureTLS bool      `json:"tls_insecure_skip_verify,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type profileDocument struct {
	SchemaVersion int             `json:"schema_version"`
	Profiles      []profileRecord `json:"profiles"`
}

func toRecord(p Profile) profileRecord {
	return profileRecord{ID: p.ID, Name: p.Name, Host: p.Host, Port: p.Port, Scheme: p.Scheme, FTPPort: p.FTPPort, TelnetPort: p.TelnetPort, Username: p.Username, Password: p.Password, Enabled: p.Enabled, InsecureTLS: p.InsecureTLS, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt}
}

func (r profileRecord) profile() Profile {
	return Profile{ID: r.ID, Name: r.Name, Host: r.Host, Port: r.Port, Scheme: r.Scheme, FTPPort: r.FTPPort, TelnetPort: r.TelnetPort, Username: r.Username, Password: r.Password, Enabled: r.Enabled, InsecureTLS: r.InsecureTLS, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

type ProfileStore struct {
	mu       sync.RWMutex
	path     string
	profiles map[string]Profile
}

func NewProfileStore(dataDir string) (*ProfileStore, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create profile directory: %w", err)
	}
	if os.PathSeparator != '\\' {
		if err := os.Chmod(dataDir, 0o700); err != nil {
			return nil, fmt.Errorf("secure profile directory: %w", err)
		}
	}
	s := &ProfileStore{path: filepath.Join(dataDir, "profiles.json"), profiles: make(map[string]Profile)}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

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
	now := time.Now().UTC()
	if p.ID == "" {
		if len(s.profiles) >= maxProfiles {
			return Profile{}, fmt.Errorf("profile limit reached (%d)", maxProfiles)
		}
		id, err := newID()
		if err != nil {
			return Profile{}, err
		}
		p.ID = id
		p.CreatedAt = now
	} else if old, ok := s.profiles[p.ID]; ok {
		p.CreatedAt = old.CreatedAt
	} else {
		return Profile{}, errors.New("profile does not exist")
	}
	p.Name = trimSpace(p.Name)
	p.Host = trimSpace(p.Host)
	p.Username = trimSpace(p.Username)
	p.UpdatedAt = now
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

// Import adds profiles as new entries. Exported IDs and timestamps are ignored so
// importing the same file twice cannot overwrite existing devices.
func (s *ProfileStore) Import(profiles []Profile) ([]Profile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(profiles) == 0 {
		return []Profile{}, nil
	}
	if len(profiles) > maxProfiles || len(s.profiles)+len(profiles) > maxProfiles {
		return nil, fmt.Errorf("profile limit reached (%d)", maxProfiles)
	}
	now := time.Now().UTC()
	imported := make([]Profile, 0, len(profiles))
	for _, p := range profiles {
		p.ID = ""
		p.CreatedAt = time.Time{}
		p.UpdatedAt = time.Time{}
		p.Password = ""
		if strings.TrimSpace(p.Username) == "" {
			p.Username = DefaultUsername
		}
		if p.Password == "" {
			p.Password = DefaultPassword
		}
		p.Name = trimSpace(p.Name)
		p.Host = trimSpace(p.Host)
		p.Username = trimSpace(p.Username)
		if err := p.Validate(); err != nil {
			return nil, fmt.Errorf("invalid imported profile %q: %w", p.Name, err)
		}
		id, err := newID()
		if err != nil {
			return nil, err
		}
		p.ID, p.CreatedAt, p.UpdatedAt = id, now, now
		imported = append(imported, p)
	}
	for _, p := range imported {
		s.profiles[p.ID] = p
	}
	if err := s.persistLocked(); err != nil {
		for _, p := range imported {
			delete(s.profiles, p.ID)
		}
		return nil, err
	}
	return imported, nil
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

func (s *ProfileStore) load() error {
	f, err := os.Open(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open profile store: %w", err)
	}
	if os.PathSeparator != '\\' {
		if err := os.Chmod(s.path, 0o600); err != nil {
			f.Close()
			return fmt.Errorf("secure existing profile store: %w", err)
		}
	}
	defer f.Close()
	limited := io.LimitReader(f, maxStoreBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return fmt.Errorf("read profile store: %w", err)
	}
	if len(data) > maxStoreBytes {
		return errors.New("profile store exceeds size limit")
	}
	var doc profileDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return fmt.Errorf("profile store is invalid JSON: %w", err)
	}
	if doc.SchemaVersion != 1 && doc.SchemaVersion != SchemaVersion {
		return fmt.Errorf("unsupported profile schema version %d", doc.SchemaVersion)
	}
	migrated := doc.SchemaVersion != SchemaVersion
	for _, record := range doc.Profiles {
		p := record.profile()
		if strings.TrimSpace(p.Username) == "" {
			p.Username = DefaultUsername
			migrated = true
		}
		if doc.SchemaVersion == 1 && p.Password == "" {
			p.Password = DefaultPassword
			migrated = true
		}
		if err := p.Validate(); err != nil {
			return fmt.Errorf("invalid saved profile %q: %w", p.ID, err)
		}
		if _, exists := s.profiles[p.ID]; exists {
			return fmt.Errorf("duplicate saved profile id %q", p.ID)
		}
		s.profiles[p.ID] = p
	}
	if migrated {
		if err := s.persistLocked(); err != nil {
			return fmt.Errorf("migrate profile store: %w", err)
		}
	}
	return nil
}

func (s *ProfileStore) persistLocked() error {
	list := make([]profileRecord, 0, len(s.profiles))
	for _, p := range s.profiles {
		list = append(list, toRecord(p))
	}
	sort.Slice(list, func(i, j int) bool { return list[i].ID < list[j].ID })
	data, err := json.MarshalIndent(profileDocument{SchemaVersion: SchemaVersion, Profiles: list}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode profile store: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".profiles-*.tmp")
	if err != nil {
		return fmt.Errorf("create temporary profile store: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("secure temporary profile store: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write profile store: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync profile store: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close profile store: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replace profile store: %w", err)
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
