package enigma2

import (
	"regexp"
	"strconv"
	"strings"
)

// ServiceKey identifies a channel independent of how its reference is padded.
type ServiceKey struct {
	SID, TSID, ONID, Namespace uint32
}

// KeyOfRef extracts the identifying fields of a DVB service reference such as
// "1:0:19:2B66:3F3:1:C00000:0:0:0:". It returns false for markers, streams and
// anything else that is not an ordinary broadcast service.
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
	// lamedb version 4: a line "sid:namespace:tsid:onid:type:number" followed by the name.
	lamedb4Header = regexp.MustCompile(`^([0-9A-Fa-f]{1,8}):([0-9A-Fa-f]{1,8}):([0-9A-Fa-f]{1,8}):([0-9A-Fa-f]{1,8}):\d+:\d+`)
	// lamedb version 5: "s:sid,namespace,tsid,onid,type,number,"name",..." (separators vary between images).
	lamedb5Service = regexp.MustCompile(`^s:([0-9A-Fa-f]{1,8})[:,]([0-9A-Fa-f]{1,8})[:,]([0-9A-Fa-f]{1,8})[:,]([0-9A-Fa-f]{1,8})[:,]\d+[:,]\d+[^"]*"([^"]*)"`)
)

// ParseLamedb returns channel names by service key from a lamedb (v4) or
// lamedb5 file. Unknown layouts simply yield fewer names.
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
			names[ServiceKey{SID: hex(m[1]), Namespace: hex(m[2]), TSID: hex(m[3]), ONID: hex(m[4])}] = m[5]
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
			name := strings.TrimSpace(lines[i+1])
			if name != "" {
				names[ServiceKey{SID: hex(m[1]), Namespace: hex(m[2]), TSID: hex(m[3]), ONID: hex(m[4])}] = name
			}
			i += 2 // skip the name and the provider/flags line
		}
	}
	return names
}

// DisplayName picks the best human-readable name for an entry.
func DisplayName(e Entry, names map[ServiceKey]string) string {
	if e.IsMarker() {
		return e.Description
	}
	if key, ok := KeyOfRef(e.Ref); ok {
		if name, found := names[key]; found {
			return name
		}
	}
	if e.Description != "" {
		return e.Description
	}
	return ""
}
