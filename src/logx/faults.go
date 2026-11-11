package logx

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type Fault struct {
	ID        string    `json:"id"`
	Level     string    `json:"level"`
	Component string    `json:"component"`
	Message   string    `json:"message"`
	Error     string    `json:"error,omitempty"`
	Route     string    `json:"route,omitempty"`
	Count     int       `json:"count"`
	First     time.Time `json:"first_seen"`
	Last      time.Time `json:"last_seen"`
	Panic     bool      `json:"panic,omitempty"`
	Stack     string    `json:"stack,omitempty"`
	Context   []Field   `json:"context,omitempty"`
	LastLogID uint64    `json:"last_log_id,omitempty"`
	Acked     bool      `json:"acked,omitempty"`
}

type FaultStats struct {
	Open   int `json:"open"`
	Errors int `json:"errors"`
	Panics int `json:"panics"`
	Total  int `json:"total"`
}

const (
	maxFaults      = 200
	faultSaveDelay = 2 * time.Second
)

type Faults struct {
	saveMu sync.Mutex
	mu     sync.Mutex
	items  map[string]*Fault
	path   string
	dirty  bool
	timer  *time.Timer
	closed bool
}

func newFaults(path string) *Faults {
	if path != "" {
		_ = os.Remove(path)
	}
	return &Faults{items: map[string]*Fault{}, path: path}
}

var (
	reNumber = regexp.MustCompile(`\b\d+\b`)
	reHex    = regexp.MustCompile(`\b[0-9a-fA-F]{8,}\b`)
	reAddr   = regexp.MustCompile(`\b\d{1,3}(\.\d{1,3}){3}(:\d+)?\b`)
	reQuoted = regexp.MustCompile(`"[^"]*"|'[^']*'`)
	reSpaces = regexp.MustCompile(`\s+`)
)

func normalize(s string) string {
	s = reQuoted.ReplaceAllString(s, "?")
	s = reAddr.ReplaceAllString(s, "<addr>")
	s = reHex.ReplaceAllString(s, "<id>")
	s = reNumber.ReplaceAllString(s, "#")
	s = reSpaces.ReplaceAllString(s, " ")
	if len(s) > 160 {
		s = s[:160]
	}
	return strings.TrimSpace(s)
}

func fingerprint(e Entry) string {
	what := e.Field("route")
	if what == "" {
		what = normalize(e.Message)
	}
	h := sha1.Sum([]byte(e.Component + "|" + what + "|" + e.Field("status") + "|" + normalize(e.Field("error"))))
	return hex.EncodeToString(h[:])[:10]
}

func (f *Faults) record(e Entry) {
	id := fingerprint(e)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.closed {
		return
	}
	it := f.items[id]
	if it == nil {
		if len(f.items) >= maxFaults {
			f.evictLocked()
		}
		it = &Fault{ID: id, Component: e.Component, Message: e.Message, First: e.Time}
		f.items[id] = it
	}
	it.Count++
	it.Last = e.Time
	it.LastLogID = e.ID
	it.Message = e.Message
	it.Error = e.Field("error")
	it.Route = e.Field("route")
	if e.Level == "error" || it.Level == "" {
		it.Level = e.Level
	}
	if e.Stack != "" {
		it.Stack = e.Stack
	}
	if e.Field("panic") == "true" {
		it.Panic = true
	}
	it.Context = it.Context[:0]
	for _, fl := range e.Fields {
		if fl.Key != "error" && fl.Key != "stack" {
			it.Context = append(it.Context, fl)
		}
	}
	it.Acked = false
	if it.Panic {
		f.saveSoonLocked(0)
	} else {
		f.saveSoonLocked(faultSaveDelay)
	}
}

func (f *Faults) evictLocked() {
	var oldest *Fault
	for _, it := range f.items {
		if oldest == nil || (it.Acked && !oldest.Acked) || (it.Acked == oldest.Acked && it.Last.Before(oldest.Last)) {
			oldest = it
		}
	}
	if oldest != nil {
		delete(f.items, oldest.ID)
	}
}

func (f *Faults) List() ([]Fault, FaultStats) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Fault, 0, len(f.items))
	var st FaultStats
	for _, it := range f.items {
		c := *it
		c.Context = append([]Field(nil), it.Context...)
		out = append(out, c)
		st.Total++
		if !it.Acked {
			st.Open++
			if it.Level == "error" {
				st.Errors++
			}
			if it.Panic {
				st.Panics++
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Last.After(out[j].Last) })
	return out, st
}

func (f *Faults) Stats() FaultStats {
	_, st := f.List()
	return st
}

func (f *Faults) Ack(id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	it := f.items[id]
	if it == nil {
		return false
	}
	it.Acked = true
	f.saveSoonLocked(faultSaveDelay)
	return true
}

func (f *Faults) AckAll() {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, it := range f.items {
		it.Acked = true
	}
	f.saveSoonLocked(faultSaveDelay)
}

func (f *Faults) Clear() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.items = map[string]*Fault{}
	f.saveSoonLocked(faultSaveDelay)
}

func (f *Faults) saveSoonLocked(delay time.Duration) {
	if f.path == "" {
		return
	}
	f.dirty = true
	if f.timer == nil {
		f.timer = time.AfterFunc(delay, f.flush)
	} else if delay == 0 {
		f.timer.Reset(0)
	}
}

func (f *Faults) flush() {
	f.saveMu.Lock()
	defer f.saveMu.Unlock()
	f.mu.Lock()
	f.timer = nil
	if !f.dirty || f.path == "" {
		f.mu.Unlock()
		return
	}
	f.dirty = false
	list := make([]Fault, 0, len(f.items))
	for _, it := range f.items {
		list = append(list, *it)
	}
	f.mu.Unlock()

	data, err := json.MarshalIndent(list, "", " ")
	if err != nil {
		return
	}
	tmp := f.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err == nil {
		_ = os.Rename(tmp, f.path)
	}
}

func (f *Faults) close() {
	f.mu.Lock()
	if f.timer != nil {
		f.timer.Stop()
		f.timer = nil
	}
	f.mu.Unlock()
	f.flush()
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
}

func faultsPath(dir string) string {
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "faults.json")
}
