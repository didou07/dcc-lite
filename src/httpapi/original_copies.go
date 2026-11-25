package httpapi

import (
	"errors"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const originalCopiesKept = 10

func (s *Server) originalCopiesDir(scope string) string {
	if s.app.DataDir == "" || scope == "" {
		return ""
	}
	return filepath.Join(s.app.DataDir, "file-backups", scope)
}

func (s *Server) keepOriginal(scope, remotePath string, content []byte) (string, error) {
	dir := s.originalCopiesDir(scope)
	if dir == "" {
		return "", errors.New("there is nowhere to keep a copy of the original file, so nothing was changed")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", errors.New("could not create the folder for the copy of the original file, so nothing was changed")
	}
	name := time.Now().Format("20060102-150405") + "-" + safeFileWord(path.Base(strings.ReplaceAll(remotePath, "\\", "/"))) + ".bak"
	file := filepath.Join(dir, name)
	if err := os.WriteFile(file, content, 0o600); err != nil {
		return "", errors.New("could not save a copy of the original file, so nothing was changed")
	}
	pruneOriginalCopies(dir)
	return file, nil
}

func pruneOriginalCopies(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".bak") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for len(names) > originalCopiesKept {
		_ = os.Remove(filepath.Join(dir, names[0]))
		names = names[1:]
	}
}
