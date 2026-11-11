package logx

import (
	"strings"
	"sync"
)

type Ring struct {
	mu      sync.RWMutex
	buf     []Entry
	next    int
	full    bool
	lastID  uint64
	dropped uint64
}

func newRing(size int) *Ring {
	if size < 100 {
		size = 100
	}
	return &Ring{buf: make([]Entry, size)}
}

func (r *Ring) add(e Entry) Entry {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastID++
	e.ID = r.lastID
	if r.full {
		r.dropped++
	}
	r.buf[r.next] = e
	r.next++
	if r.next == len(r.buf) {
		r.next = 0
		r.full = true
	}
	return e
}

type Query struct {
	Since      uint64
	MinLevel   int
	Components map[string]bool
	Text       string
	Limit      int
}

func levelRank(name string) int {
	for i, n := range LevelNames {
		if n == name {
			return i
		}
	}
	return 0
}

func (q Query) match(e Entry) bool {
	if e.ID <= q.Since {
		return false
	}
	if levelRank(e.Level) < q.MinLevel {
		return false
	}
	if len(q.Components) > 0 && !q.Components[e.Component] {
		return false
	}
	if q.Text != "" {
		hay := strings.ToLower(e.Component + " " + e.Message)
		for _, f := range e.Fields {
			hay += " " + strings.ToLower(f.Key+"="+f.Value)
		}
		if !strings.Contains(hay, strings.ToLower(q.Text)) {
			return false
		}
	}
	return true
}

func (r *Ring) Snapshot(q Query) (entries []Entry, lastID uint64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := r.next
	start := 0
	if r.full {
		n = len(r.buf)
		start = r.next
	}
	for i := 0; i < n; i++ {
		e := r.buf[(start+i)%len(r.buf)]
		if q.match(e) {
			entries = append(entries, e)
		}
	}
	if q.Limit > 0 && len(entries) > q.Limit {
		entries = entries[len(entries)-q.Limit:]
	}
	return entries, r.lastID
}

func (r *Ring) Counts() (levels map[string]int, components map[string]int) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	levels, components = map[string]int{}, map[string]int{}
	n := r.next
	if r.full {
		n = len(r.buf)
	}
	for i := 0; i < n; i++ {
		levels[r.buf[i].Level]++
		components[r.buf[i].Component]++
	}
	return
}

func (r *Ring) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.next, r.full = 0, false
	for i := range r.buf {
		r.buf[i] = Entry{}
	}
}

func (r *Ring) Capacity() int { return len(r.buf) }
