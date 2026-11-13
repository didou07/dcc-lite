package remoteaccess

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"sync"
)

const (
	MinPasswordLength = 10
	maxPasswordLength = 256
	pbkdf2Iterations  = 600_000
)

var ErrPasswordNotSet = errors.New("remote access password is not set")

type PasswordStore interface {
	PasswordRecord() (salt, hash string, iterations int)
	SavePasswordRecord(salt, hash string, iterations int) error
}

type credentialRecord struct {
	Salt       string
	Hash       string
	Iterations int
}

type Credentials struct {
	mu       sync.Mutex
	store    PasswordStore
	record   credentialRecord
	set      bool
	revision uint64
}

func OpenCredentials(store PasswordStore) (*Credentials, error) {
	c := &Credentials{store: store}
	salt, hash, iterations := store.PasswordRecord()
	if salt == "" && hash == "" && iterations == 0 {
		return c, nil
	}
	if salt == "" || hash == "" || iterations != pbkdf2Iterations {
		return nil, errors.New("the remote password record is invalid for this settings format; reset the remote password or create a new settings file")
	}
	saltBytes, saltErr := base64.StdEncoding.DecodeString(salt)
	hashBytes, hashErr := base64.StdEncoding.DecodeString(hash)
	if saltErr != nil || len(saltBytes) != 16 || hashErr != nil || len(hashBytes) != 32 {
		return nil, errors.New("the remote password record has invalid salt or hash data")
	}
	c.record = credentialRecord{Salt: salt, Hash: hash, Iterations: iterations}
	c.set = true
	c.revision = 1
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
	return c.save(password)
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
	if err := c.save(password); err != nil {
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

func (c *Credentials) save(password string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	hash, err := pbkdf2.Key(sha256.New, password, salt, pbkdf2Iterations, 32)
	if err != nil {
		return err
	}
	record := credentialRecord{
		Salt: base64.StdEncoding.EncodeToString(salt), Hash: base64.StdEncoding.EncodeToString(hash),
		Iterations: pbkdf2Iterations,
	}
	if err := c.store.SavePasswordRecord(record.Salt, record.Hash, record.Iterations); err != nil {
		return err
	}
	c.record, c.set = record, true
	c.revision++
	return nil
}

func (c *Credentials) Verify(password string) bool {
	valid, _ := c.verify(password)
	return valid
}

func (c *Credentials) verify(password string) (bool, uint64) {
	c.mu.Lock()
	record, set, revision := c.record, c.set, c.revision
	c.mu.Unlock()
	if !set || len(password) > maxPasswordLength {
		return false, revision
	}
	salt, err := base64.StdEncoding.DecodeString(record.Salt)
	if err != nil {
		return false, revision
	}
	want, err := base64.StdEncoding.DecodeString(record.Hash)
	if err != nil {
		return false, revision
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, record.Iterations, len(want))
	if err != nil || subtle.ConstantTimeCompare(got, want) != 1 {
		return false, revision
	}
	return c.revisionMatches(revision), revision
}

func (c *Credentials) revisionMatches(revision uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.set && c.revision == revision
}
