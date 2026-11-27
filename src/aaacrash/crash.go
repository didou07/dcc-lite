// Package aaacrash captures crashes that happen before the logger exists.
package crashlog

import (
	"os"
	"path/filepath"
	"strings"
)

const stderrFile = "crash-stderr.log"
const maxStderrBytes = 1 << 20

// Dir returns the log folder (honours --log-dir, --data-dir, --portable).
func Dir(args []string) string {
	var logDir, dataDir string
	portable := false
	for i, a := range args {
		name, val, hasVal := strings.Cut(strings.TrimLeft(a, "-"), "=")
		if !strings.HasPrefix(a, "-") {
			continue
		}
		next := func() string {
			if hasVal {
				return val
			}
			if i+1 < len(args) {
				return args[i+1]
			}
			return ""
		}
		switch name {
		case "log-dir":
			logDir = next()
		case "data-dir":
			dataDir = next()
		case "portable":
			portable = !hasVal || val == "true"
		}
	}
	if strings.EqualFold(logDir, "off") {
		logDir = ""
	}
	switch {
	case logDir != "":
		return logDir
	case dataDir != "":
		return filepath.Join(dataDir, "logs")
	case portable:
		if exe, err := os.Executable(); err == nil {
			return filepath.Join(filepath.Dir(exe), "data", "logs")
		}
	}
	if base, err := os.UserConfigDir(); err == nil {
		return filepath.Join(base, "DCCLite", "logs")
	}
	return filepath.Join(os.TempDir(), "DCCLite-logs")
}

// openStderrFile opens the crash file, starting over when it has grown large.
func openStderrFile() (*os.File, error) {
	dir := Dir(os.Args[1:])
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, stderrFile)
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if st, err := os.Stat(path); err == nil && st.Size() > maxStderrBytes {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	return os.OpenFile(path, flags, 0o600)
}
