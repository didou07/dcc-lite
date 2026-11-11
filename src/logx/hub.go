package logx

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"time"
)

type Options struct {
	Console      io.Writer
	ConsoleLevel slog.Level
	FileLevel    slog.Level
	Dir          string
	FileName     string
	JSON         bool
	RingSize     int
	MaxFileSize  int64
	KeepFiles    int
}

type Hub struct {
	consoleLevel atomic.Int64
	fileLevel    atomic.Int64

	writeMu sync.Mutex
	console io.Writer
	file    *rotator
	json    bool

	ring   *Ring
	faults *Faults

	dir      string
	filePath string
	started  time.Time
}

var (
	current atomic.Pointer[Hub]
	initMu  sync.Mutex
)

func Init(o Options) (*Hub, error) {
	initMu.Lock()
	defer initMu.Unlock()

	h := &Hub{console: o.Console, json: o.JSON, dir: o.Dir, started: time.Now()}
	if h.console == nil {
		h.console = os.Stderr
	}
	h.consoleLevel.Store(int64(o.ConsoleLevel))
	h.fileLevel.Store(int64(o.FileLevel))
	if o.RingSize == 0 {
		o.RingSize = 3000
	}
	h.ring = newRing(o.RingSize)

	fileErr := h.openFile(o)
	h.faults = newFaults(faultsPath(o.Dir))

	if old := current.Swap(h); old != nil {
		old.Close()
	}
	slog.SetDefault(For("app"))
	return h, fileErr
}

func (h *Hub) openFile(o Options) error {
	if o.Dir == "" {
		return nil
	}
	name := o.FileName
	if name == "" {
		name = "dcclite.log"
	}
	size, keep := o.MaxFileSize, o.KeepFiles
	if size <= 0 {
		size = 5 << 20
	}
	if keep <= 0 {
		keep = 5
	}
	path := filepath.Join(o.Dir, name)
	r, err := newRotator(path, size, keep)
	if err != nil {
		return err
	}
	h.file = r
	h.filePath = path
	return nil
}

func Default() *Hub {
	if h := current.Load(); h != nil {
		return h
	}
	h, _ := Init(Options{})
	return h
}

func For(component string) *slog.Logger {
	return slog.New(&handler{component: component})
}

func (h *Hub) For(component string) *slog.Logger {
	return slog.New(&handler{hub: h, component: component})
}

func (h *Hub) Ring() *Ring      { return h.ring }
func (h *Hub) Faults() *Faults  { return h.faults }
func (h *Hub) FilePath() string { return h.filePath }
func (h *Hub) Dir() string      { return h.dir }
func (h *Hub) JSON() bool       { return h.json }

func (h *Hub) ConsoleLevel() slog.Level     { return slog.Level(h.consoleLevel.Load()) }
func (h *Hub) FileLevel() slog.Level        { return slog.Level(h.fileLevel.Load()) }
func (h *Hub) SetConsoleLevel(l slog.Level) { h.consoleLevel.Store(int64(l)) }
func (h *Hub) SetFileLevel(l slog.Level)    { h.fileLevel.Store(int64(l)) }

func (h *Hub) ringLevel() slog.Level {
	l := LevelDebug
	if c := h.ConsoleLevel(); c < l {
		l = c
	}
	if f := h.FileLevel(); h.file != nil && f < l {
		l = f
	}
	return l
}

func (h *Hub) minLevel() slog.Level {
	l := h.ringLevel()
	if c := h.ConsoleLevel(); c < l {
		l = c
	}
	return l
}

func (h *Hub) SessionStart(version string) {
	if h.file == nil {
		return
	}
	now := time.Now()
	var line string
	if h.json {
		b, _ := json.Marshal(map[string]any{"session_start": now, "version": version, "os": runtime.GOOS, "arch": runtime.GOARCH, "pid": os.Getpid()})
		line = string(b) + "\n"
	} else {
		line = fmt.Sprintf("===== DCC Lite %s started %s (%s/%s, %s, pid %d) =====\n",
			version, now.Format(timeLayoutFile), runtime.GOOS, runtime.GOARCH, runtime.Version(), os.Getpid())
	}
	_, _ = h.file.Write([]byte(line))
}

func (h *Hub) Close() {
	h.faults.close()
	if h.file != nil {
		_ = h.file.Close()
	}
}

func Close() {
	if h := current.Load(); h != nil {
		h.Close()
	}
}

func (h *Hub) emit(e Entry) {
	if e.level >= h.ringLevel() {
		e = h.ring.add(e)
	}
	if e.level >= LevelWarn {
		h.faults.record(e)
	}
	if e.level >= h.ConsoleLevel() {
		line := formatLine(e, false)
		h.writeMu.Lock()
		_, _ = io.WriteString(h.console, line)
		h.writeMu.Unlock()
	}
	if h.file != nil && e.level >= h.FileLevel() {
		if h.json {
			b, _ := json.Marshal(e)
			_, _ = h.file.Write(append(b, '\n'))
		} else {
			_, _ = h.file.Write([]byte(formatLine(e, true)))
		}
	}
}
