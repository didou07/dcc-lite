package enigma2

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type ServiceKey struct {
	SID, TSID, ONID, Namespace uint32
}

func KeyOfRef(ref string) (ServiceKey, bool) {
	fields := strings.Split(ref, ":")
	if len(fields) < 7 || fields[0] != "1" {
		return ServiceKey{}, false
	}
	var v [4]uint32
	for n, idx := range []int{3, 4, 5, 6} {
		parsed, err := strconv.ParseUint(fields[idx], 16, 32)
		if err != nil {
			return ServiceKey{}, false
		}
		v[n] = uint32(parsed)
	}
	if v[0] == 0 && v[1] == 0 && v[2] == 0 {
		return ServiceKey{}, false
	}
	return ServiceKey{SID: v[0], TSID: v[1], ONID: v[2], Namespace: v[3]}, true
}

var (
	lamedb4Header  = regexp.MustCompile(`^([0-9A-Fa-f]{1,8}):([0-9A-Fa-f]{1,8}):([0-9A-Fa-f]{1,8}):([0-9A-Fa-f]{1,8}):\d+:\d+`)
	lamedb5Service = regexp.MustCompile(`^s:([0-9A-Fa-f]{1,8})[:,]([0-9A-Fa-f]{1,8})[:,]([0-9A-Fa-f]{1,8})[:,]([0-9A-Fa-f]{1,8})[:,]\d+[:,]\d+[^"]*"([^"]*)"`)
)

func ParseLamedb(text string) map[ServiceKey]string {
	names := make(map[ServiceKey]string)
	lines := splitLines(text)
	hex := func(s string) uint32 {
		v, _ := strconv.ParseUint(s, 16, 32)
		return uint32(v)
	}
	inServices := false
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if m := lamedb5Service.FindStringSubmatch(line); m != nil {
			if name := CleanName(m[5]); name != "" {
				names[ServiceKey{SID: hex(m[1]), Namespace: hex(m[2]), TSID: hex(m[3]), ONID: hex(m[4])}] = name
			}
			continue
		}
		switch line {
		case "services":
			inServices = true
			continue
		case "end":
			inServices = false
			continue
		}
		if !inServices {
			continue
		}
		if m := lamedb4Header.FindStringSubmatch(line); m != nil && i+1 < len(lines) {
			name := CleanName(lines[i+1])
			if name != "" {
				names[ServiceKey{SID: hex(m[1]), Namespace: hex(m[2]), TSID: hex(m[3]), ONID: hex(m[4])}] = name
			}
			i += 2
		}
	}
	return names
}

func CleanName(name string) string {
	name = strings.Map(func(r rune) rune {
		switch r {
		case 0x1A, 0x86, 0x87, 0x8A:
			return -1
		}
		return r
	}, name)
	return strings.TrimSpace(name)
}

func SplitDescription(description string) (name, provider string) {
	name, provider, _ = strings.Cut(description, "•")
	if i := strings.Index(provider, "•"); i >= 0 {
		provider = provider[:i]
	}
	return CleanName(name), CleanName(provider)
}

func DisplayName(e Entry, names map[ServiceKey]string) string {
	if name, _ := SplitDescription(e.Description); name != "" {
		return name
	}
	if e.IsMarker() {
		return ""
	}
	if key, ok := KeyOfRef(e.Ref); ok {
		if name, found := names[key]; found {
			return name
		}
	}
	return ""
}

type Service struct {
	Ref      string `json:"ref"`
	Name     string `json:"name"`
	Provider string `json:"provider,omitempty"`
	Type     int    `json:"type"`
	Radio    bool   `json:"radio"`
}

func radioType(t int) bool { return t == 2 || t == 10 }

func IsTVType(t int) bool {
	switch t {
	case 1, 17, 22, 25, 31, 32, 134, 195:
		return true
	}
	return false
}

func MakeRef(key ServiceKey, serviceType int) string {
	return fmt.Sprintf("1:0:%X:%X:%X:%X:%X:0:0:0:", serviceType, key.SID, key.TSID, key.ONID, key.Namespace)
}

func ParseServices(text string) []Service {
	var out []Service
	seen := map[string]bool{}
	hex := func(s string) uint32 {
		v, _ := strconv.ParseUint(s, 16, 32)
		return uint32(v)
	}
	add := func(key ServiceKey, serviceType int, name, provider string) {
		ref := MakeRef(key, serviceType)
		name, provider = CleanName(name), CleanName(provider)
		if name == "" || seen[ref] {
			return
		}
		seen[ref] = true
		out = append(out, Service{Ref: ref, Name: name, Provider: provider, Type: serviceType, Radio: radioType(serviceType)})
	}
	lines := splitLines(text)
	inServices := false
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		if m := lamedb5Full.FindStringSubmatch(line); m != nil {
			t, _ := strconv.Atoi(m[5])
			provider := ""
			if p := lamedb5Provider.FindStringSubmatch(line); p != nil {
				provider = p[1]
			}
			add(ServiceKey{SID: hex(m[1]), Namespace: hex(m[2]), TSID: hex(m[3]), ONID: hex(m[4])}, t, m[6], provider)
			continue
		}
		switch line {
		case "services":
			inServices = true
			continue
		case "end":
			inServices = false
			continue
		}
		if !inServices {
			continue
		}
		if m := lamedb4Typed.FindStringSubmatch(line); m != nil && i+1 < len(lines) {
			t, _ := strconv.Atoi(m[5])
			name := lines[i+1]
			provider := ""
			if i+2 < len(lines) {
				if p := lamedb4Provider.FindStringSubmatch(strings.TrimSpace(lines[i+2])); p != nil {
					provider = p[1]
				}
			}
			add(ServiceKey{SID: hex(m[1]), Namespace: hex(m[2]), TSID: hex(m[3]), ONID: hex(m[4])}, t, name, provider)
			i += 2
		}
	}
	return out
}

var (
	lamedb4Typed    = regexp.MustCompile(`^([0-9A-Fa-f]{1,8}):([0-9A-Fa-f]{1,8}):([0-9A-Fa-f]{1,8}):([0-9A-Fa-f]{1,8}):(\d+):\d+`)
	lamedb4Provider = regexp.MustCompile(`^p:([^,]*)`)
	lamedb5Full     = regexp.MustCompile(`^s:([0-9A-Fa-f]{1,8})[:,]([0-9A-Fa-f]{1,8})[:,]([0-9A-Fa-f]{1,8})[:,]([0-9A-Fa-f]{1,8})[:,](\d+)[:,]\d+[^"]*"([^"]*)"`)
	lamedb5Provider = regexp.MustCompile(`[:,]p:([^,:]*)`)
)
