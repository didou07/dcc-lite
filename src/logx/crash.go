package logx

import (
	"fmt"
	"log/slog"
	"runtime/debug"
	"strings"
)

const (
	maxStackBytes  = 6000
	maxStackFrames = 12
)

func Panic(log *slog.Logger, recovered any, what string, args ...any) {
	args = append(args, "error", fmt.Sprint(recovered), "panic", true, "stack", string(debug.Stack()))
	log.Error(what+" crashed", args...)
}

func Recover(log *slog.Logger, what string) {
	if r := recover(); r != nil {
		Panic(log, r, what)
	}
}

func Go(log *slog.Logger, what string, fn func()) {
	go func() {
		defer Recover(log, what)
		fn()
	}()
}

func cleanStack(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	start := 0
	for i, l := range lines {
		if strings.HasPrefix(l, "panic(") || strings.Contains(l, "runtime.gopanic") {
			start = i + 2
			break
		}
	}
	if start >= len(lines) {
		start = 0
	}
	out := make([]string, 0, len(lines))
	for _, l := range lines[start:] {
		if strings.HasPrefix(l, "\t") {
			l = "\t" + shortPath(strings.TrimPrefix(l, "\t"))
		}
		out = append(out, l)
	}
	text := strings.Join(out, "\n")
	if len(text) > maxStackBytes {
		text = text[:maxStackBytes] + "\n\t… (truncated)"
	}
	return text
}

func shortPath(l string) string {
	i := strings.LastIndexAny(l, `/\`)
	if i < 0 {
		return l
	}
	j := strings.LastIndexAny(l[:i], `/\`)
	if j < 0 {
		return l
	}
	return l[j+1:]
}

func compactStack(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	var frames []string
	fn := ""
	for _, l := range lines {
		if !strings.HasPrefix(l, "\t") {
			fn = l
			continue
		}
		loc := strings.TrimSpace(l)
		if i := strings.Index(loc, " +0x"); i >= 0 {
			loc = loc[:i]
		}
		frames = append(frames, shortFunc(fn)+" "+loc)
		fn = ""
	}
	if len(frames) == 0 {
		for _, l := range lines {
			if l = strings.TrimSpace(l); l != "" {
				frames = append(frames, l)
			}
		}
	}
	if len(frames) > maxStackFrames {
		frames = frames[:maxStackFrames]
	}
	return strings.Join(frames, " > ")
}

func shortFunc(fn string) string {
	if i := strings.LastIndex(fn, "("); i > 0 {
		fn = fn[:i]
	}
	if i := strings.LastIndex(fn, "/"); i >= 0 {
		fn = fn[i+1:]
	}
	if fn == "" {
		return "?"
	}
	return fn
}
