package httpapi

import (
	"strings"
	"sync"
	"time"
)

// Picon lookup, modelled on the Enigma2 picon renderer that OpenWebif uses.
// A receiver stores logos in its picon folder under one of several names, so a
// single exact name misses many channels. The names are tried in this order:
//
//  1. the service reference        1_0_19_283D_3FB_1_C00000_0_0_0.png
//  2. the same with service type 1 (HD, UHD and other types share the SD logo)
//  3. the channel name, simplified bbcone.png
//  4. that name without a quality suffix (fhd, uhd, hd, sd, 4k)
//
// Only names built here are ever requested from the receiver, and every name
// consists of [0-9A-Za-z_] only, so nothing the browser sends can leave /picon/.

// piconCandidates returns the picon file names to try for a channel, best first.
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
	// Service type is the third field (index 2); 2 is radio and keeps its own logo.
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

// piconName simplifies a channel name the way Enigma2 does for name based picons:
// accents dropped, "&" "+" "*" spelled out, lower case, letters and digits only.
// A name with no Latin letters (for example Arabic) simplifies to "".
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

// latinBase maps accented Latin letters to the ASCII letter Unicode
// decomposition leaves (what Python's NFKD + ASCII "ignore" gives Enigma2).
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

// piconMemo remembers which file name worked (or that none did) for a channel,
// so scrolling through a long list asks the receiver as little as possible.
type piconMemo struct {
	mu      sync.Mutex
	entries map[string]piconMemoEntry
}

type piconMemoEntry struct {
	file    string // "" = no picon exists for this channel
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

// forget drops everything remembered (used when the receiver's channel list is reloaded).
func (m *piconMemo) forget() {
	m.mu.Lock()
	m.entries = map[string]piconMemoEntry{}
	m.mu.Unlock()
}
