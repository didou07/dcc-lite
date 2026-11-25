package httpapi

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/terminal"
)

type shellRunner struct {
	profile config.Profile
}

func (r shellRunner) run(ctx context.Context, timeout time.Duration, command string, max int) (string, int, error) {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return terminal.RunCommand(c, r.profile.Host, r.profile.EffectiveTelnetPort(), telnetUser(r.profile), r.profile.Password, command, max)
}

var backupBusy sync.Map

func (s *Server) denyRemoteBackup(w http.ResponseWriter) bool {
	if s.remote == nil {
		return false
	}
	writeJSON(w, http.StatusForbidden, map[string]any{
		"error": "Backups are only available on the computer that runs DCC Lite.",
		"code":  "backup_unavailable_remote",
	})
	return true
}

func fileMD5(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := md5.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

var unsafeFileWord = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func safeFileWord(s string) string {
	s = strings.Trim(unsafeFileWord.ReplaceAllString(s, "-"), "-.")
	if s == "" {
		return "file"
	}
	if len(s) > 24 {
		s = s[:24]
	}
	return s
}
