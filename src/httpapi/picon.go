package httpapi

import (
	"strings"
	"sync"
	"time"
)

func piconCandidates(ref, name string) []string {
	var out []string
	add := func(file string) {
		if file == "" {
			return
		}
		for _, have := range out {
			if have == file {
				return
			}
		}
		out = append(out, file)
	}
	first := piconFile(ref)
	if first == "" {
		return nil
	}
	add(first)

	fields := strings.Split(strings.TrimSuffix(first, ".png"), "_")
	if len(fields) == 10 && fields[2] != "2" && fields[2] != "1" {
		fields[2] = "1"
		add(strings.Join(fields, "_") + ".png")
	}
	simple := piconName(name)
	if simple != "" {
		add(simple + ".png")
		trimmed := strings.TrimSpace(simple)
		for _, suffix := range []string{"fhd", "uhd", "hd", "sd", "4k"} {
			if strings.HasSuffix(trimmed, suffix) && len(trimmed) > len(suffix) {
				add(strings.TrimSuffix(trimmed, suffix) + ".png")
				break
			}
		}
	}
	return out
}

func piconName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 200 {
		return ""
	}
	name = strings.NewReplacer("&", "and", "+", "plus", "*", "star").Replace(name)
	var b strings.Builder
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			if base, ok := latinBase[r]; ok {
				b.WriteByte(base)
			}
		}
	}
	return b.String()
}

var latinBase = func() map[rune]byte {
	m := map[rune]byte{}
	for base, letters := range map[byte]string{
		'a': "àáâãäåāăąǎ", 'c': "çćĉċč", 'd': "ď", 'e': "èéêëēĕėęě", 'g': "ĝğġģ", 'h': "ĥ",
		'i': "ìíîïĩīĭįǐ", 'j': "ĵ", 'k': "ķ", 'l': "ĺļľ", 'n': "ñńņň", 'o': "òóôõöōŏőǒ",
		'r': "ŕŗř", 's': "śŝşš", 't': "ţť", 'u': "ùúûüũūŭůűųǔ", 'w': "ŵ", 'y': "ýÿŷ", 'z': "źżž",
	} {
		for _, r := range letters {
			m[r] = base
		}
	}
	return m
}()

type piconMemo struct {
	mu      sync.Mutex
	entries map[string]piconMemoEntry
}

type piconMemoEntry struct {
	file    string
	expires time.Time
}

const (
	piconHitTTL  = 30 * time.Minute
	piconMissTTL = 5 * time.Minute
	piconMemoMax = 4000
)

var piconCache = &piconMemo{entries: map[string]piconMemoEntry{}}

func (m *piconMemo) get(key string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[key]
	if !ok || time.Now().After(e.expires) {
		delete(m.entries, key)
		return "", false
	}
	return e.file, true
}

func (m *piconMemo) put(key, file string) {
	ttl := piconHitTTL
	if file == "" {
		ttl = piconMissTTL
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.entries) >= piconMemoMax {
		now := time.Now()
		for k, e := range m.entries {
			if now.After(e.expires) {
				delete(m.entries, k)
			}
		}
		if len(m.entries) >= piconMemoMax {
			m.entries = map[string]piconMemoEntry{}
		}
	}
	m.entries[key] = piconMemoEntry{file: file, expires: time.Now().Add(ttl)}
}

func (m *piconMemo) forget() {
	m.mu.Lock()
	m.entries = map[string]piconMemoEntry{}
	m.mu.Unlock()
}
