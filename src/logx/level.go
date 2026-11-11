package logx

import (
	"fmt"
	"log/slog"
	"strings"
)

const (
	LevelTrace = slog.Level(-8)
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError
)

var LevelNames = []string{"trace", "debug", "info", "warn", "error"}

func ParseLevel(value string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "trace":
		return LevelTrace, nil
	case "debug":
		return LevelDebug, nil
	case "info", "":
		return LevelInfo, nil
	case "warn", "warning":
		return LevelWarn, nil
	case "error", "err":
		return LevelError, nil
	}
	return LevelInfo, fmt.Errorf("unknown log level %q (use trace, debug, info, warn or error)", value)
}

func LevelName(l slog.Level) string {
	switch {
	case l <= LevelTrace:
		return "trace"
	case l <= LevelDebug:
		return "debug"
	case l <= LevelInfo:
		return "info"
	case l <= LevelWarn:
		return "warn"
	default:
		return "error"
	}
}

func levelTag(l slog.Level) string {
	switch LevelName(l) {
	case "trace":
		return "TRACE"
	case "debug":
		return "DEBUG"
	case "info":
		return "INFO "
	case "warn":
		return "WARN "
	}
	return "ERROR"
}
