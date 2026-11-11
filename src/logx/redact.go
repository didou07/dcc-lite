package logx

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

var sensitiveKeys = map[string]bool{
	"password": true, "pass": true, "passwd": true, "pwd": true, "secret": true,
	"token": true, "cookie": true, "authorization": true, "credential": true,
	"api_key": true, "apikey": true, "private_key": true, "key_pem": true,
}

var (
	secretPair = regexp.MustCompile(`(?i)\b(password|passwd|pwd|token|secret|authorization)(\s*[=:]\s*)("[^"]*"|\S+)`)
	urlUser    = regexp.MustCompile(`(?i)(\w+://[^/\s:@]+):[^/\s@]+@`)
)

var homeDirs = func() []string {
	dir, err := os.UserHomeDir()
	if err != nil || len(dir) < 4 {
		return nil
	}
	return []string{dir, filepath.ToSlash(dir), strings.ReplaceAll(dir, `\`, `\\`)}
}()

func isSensitiveKey(key string) bool {
	return sensitiveKeys[strings.ToLower(key)]
}

func Redact(s string) string {
	if s == "" {
		return s
	}
	s = secretPair.ReplaceAllString(s, "${1}${2}***")
	s = urlUser.ReplaceAllString(s, "${1}:***@")
	return s
}

func redactHome(s string) string {
	for _, dir := range homeDirs {
		s = strings.ReplaceAll(s, dir, "~")
	}
	return s
}
