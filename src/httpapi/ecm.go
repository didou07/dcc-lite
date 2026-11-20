package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ---- Control words (ecm.info) ----
var caSystems = []struct {
	lo, hi int
	name   string
}{
	{0x0100, 0x01FF, "SECA / Mediaguard"}, {0x0500, 0x05FF, "Viaccess"}, {0x0600, 0x06FF, "Irdeto"},
	{0x0900, 0x09FF, "NDS / VideoGuard"}, {0x0B00, 0x0BFF, "Conax"}, {0x0D00, 0x0DFF, "Cryptoworks"},
	{0x1700, 0x17FF, "BetaCrypt"}, {0x1800, 0x18FF, "Nagravision"}, {0x2600, 0x26FF, "BISS"},
	{0x4AE0, 0x4AE1, "DRE-Crypt"}, {0x0E00, 0x0EFF, "PowerVu"}, {0x4A00, 0x4AFF, "Tongfang / other"},
}

func caSystemName(caid int) string {
	for _, s := range caSystems {
		if caid >= s.lo && caid <= s.hi {
			return s.name
		}
	}
	return ""
}

var hexOnly = regexp.MustCompile(`[^0-9A-Fa-f]`)

func formatCW(raw string) string {
	hex := strings.ToUpper(hexOnly.ReplaceAllString(raw, ""))
	if len(hex) != 16 {
		return ""
	}
	parts := make([]string, 0, 8)
	for i := 0; i < 16; i += 2 {
		parts = append(parts, hex[i:i+2])
	}
	return strings.Join(parts, " ")
}

type controlWord struct {
	Name string `json:"name"`
	Hex  string `json:"hex"`
}

func parseControlWords(text string) []controlWord {
	raw := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		i := strings.IndexAny(line, ":=")
		if i <= 0 || i > 12 {
			continue
		}
		key := strings.ToLower(strings.NewReplacer(" ", "", "_", "", "-", "").Replace(line[:i]))
		switch key {
		case "cw0", "cweven", "even", "cwe":
			key = "cw0"
		case "cw1", "cwodd", "odd", "cwo":
			key = "cw1"
		case "cw", "cws", "controlword":
			key = "cw"
		default:
			continue
		}
		if _, seen := raw[key]; !seen {
			raw[key] = strings.TrimSpace(line[i+1:])
		}
	}
	var out []controlWord
	if v := formatCW(raw["cw0"]); v != "" {
		out = append(out, controlWord{"CW0 (even)", v})
	}
	if v := formatCW(raw["cw1"]); v != "" {
		out = append(out, controlWord{"CW1 (odd)", v})
	}
	if len(out) == 0 {
		// Some emulators print both words on one "cw:" line.
		if hex := strings.ToUpper(hexOnly.ReplaceAllString(raw["cw"], "")); len(hex) == 32 {
			out = append(out, controlWord{"CW0 (even)", formatCW(hex[:16])}, controlWord{"CW1 (odd)", formatCW(hex[16:])})
		}
	}
	return out
}

func ecmCAID(text string) int {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if i := strings.Index(line, ":"); i > 0 && strings.EqualFold(strings.TrimSpace(line[:i]), "caid") {
			value := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(line[i+1:])), "0x")
			if fields := strings.Fields(value); len(fields) > 0 {
				value = fields[0]
			}
			if n, err := strconv.ParseInt(value, 16, 32); err == nil {
				return int(n)
			}
		}
	}
	return 0
}

func (s *Server) deviceLiveECM(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 8*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, web.fileAddress("/tmp/ecm.info"), 8192)
	if err != nil || status == http.StatusUnauthorized || status == http.StatusForbidden {
		if writeWebStatus(w, web, status, err) {
			return
		}
	}
	fields := []infoPair{}
	words := []controlWord{}
	available := false
	rawText := ""
	if status == http.StatusOK {
		if text := string(body); !strings.HasPrefix(text, "File '") && strings.TrimSpace(text) != "" {
			available = true
			rawText = cleanText(text, 500)
			if f := parseECM(text); f != nil {
				fields = f
			}
			if caid := ecmCAID(text); caid > 0 {
				if name := caSystemName(caid); name != "" {
					fields = append([]infoPair{{"CA system", name}}, fields...)
				}
			}
			words = parseControlWords(text)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"available": available, "fields": fields, "cw": words, "raw": rawText, "at": time.Now().UnixMilli()})
}
