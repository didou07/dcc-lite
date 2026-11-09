package remoteaccess

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	MinPasswordLength = 10
	maxPasswordLength = 256
	pbkdf2Iterations  = 310_000
	credentialsFile   = "remote.json"
)

var ErrPasswordNotSet = errors.New("remote access password is not set")

type credentialRecord struct {
	Version    int       `json:"version"`
	Salt       string    `json:"salt"`
	Hash       string    `json:"hash"`
	Iterations int       `json:"iterations"`
	UpdatedAt  time.Time `json:"updated_at"`
}

type Credentials struct {
	mu     sync.Mutex
	path   string
	record credentialRecord
	set    bool
}

func OpenCredentials(dir string) (*Credentials, error) {
	c := &Credentials{path: filepath.Join(dir, credentialsFile)}
	data, err := os.ReadFile(c.path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &c.record); err != nil {
		return nil, fmt.Errorf("read %s: %w", credentialsFile, err)
	}
	if c.record.Salt == "" || c.record.Hash == "" || c.record.Iterations < 100_000 {
		return nil, fmt.Errorf("%s is incomplete or uses weak settings; reset the remote password", credentialsFile)
	}
	c.set = true
	return c, nil
}

func (c *Credentials) Configured() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.set
}

func (c *Credentials) Set(password string) error {
	if err := ValidatePassword(password); err != nil {
		return err
	}
	return c.store(password)
}

func (c *Credentials) Generate() (string, error) {
	const alphabet = "abcdefghjkmnpqrstuvwxyz23456789"
	const length = 20

	limit := byte(256 - 256%len(alphabet))
	var b strings.Builder
	buffer := make([]byte, 64)
	for count := 0; count < length; {
		if _, err := rand.Read(buffer); err != nil {
			return "", err
		}
		for _, v := range buffer {
			if v >= limit {
				continue
			}
			if count > 0 && count%5 == 0 {
				b.WriteByte('-')
			}
			b.WriteByte(alphabet[int(v)%len(alphabet)])
			count++
			if count == length {
				break
			}
		}
	}
	password := b.String()
	if err := c.store(password); err != nil {
		return "", err
	}
	return password, nil
}

func ValidatePassword(password string) error {
	if len(password) < MinPasswordLength {
		return fmt.Errorf("the remote password must be at least %d characters", MinPasswordLength)
	}
	if len(password) > maxPasswordLength {
		return fmt.Errorf("the remote password must be at most %d characters", maxPasswordLength)
	}
	return nil
}

func (c *Credentials) store(password string) error {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return err
	}
	record := credentialRecord{
		Version: 1, Salt: base64.StdEncoding.EncodeToString(salt), Hash: base64.StdEncoding.EncodeToString(hash),
		Iterations: pbkdf2Iterations, UpdatedAt: time.Now().UTC(),
	}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(c.path), ".remote-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil && !errors.Is(err, os.ErrPermission) {

		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, c.path); err != nil {
		return err
	}
	c.mu.Lock()
	c.record, c.set = record, true
	c.mu.Unlock()
	return nil
}

func (c *Credentials) Verify(password string) bool {
	c.mu.Lock()
	record, set := c.record, c.set
	c.mu.Unlock()
	if !set || len(password) > maxPasswordLength {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(record.Salt)
	if err != nil {
		return false
	}
	want, err := base64.StdEncoding.DecodeString(record.Hash)
	if err != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, record.Iterations, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}
