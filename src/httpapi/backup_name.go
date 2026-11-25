package httpapi

import (
	"context"
	"regexp"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/config"
)

type imageIdentity struct {
	Name    string
	Version string
	Build   string
	Box     string
}

const imageProbeCommand = `for f in /etc/image-version /etc/issue /proc/stb/info/boxtype /proc/stb/info/model; do echo "##$f"; head -n 12 "$f" 2>/dev/null; done`

var unsafeNamePart = regexp.MustCompile(`[^A-Za-z0-9._+]+`)

func namePart(s string, limit int) string {
	s = strings.Trim(unsafeNamePart.ReplaceAllString(strings.TrimSpace(s), "-"), "-.")
	if len(s) > limit {
		s = strings.Trim(s[:limit], "-.")
	}
	return s
}

func issueName(lines []string) string {
	for _, line := range lines {
		for _, word := range strings.Fields(line) {
			switch strings.ToLower(word) {
			case "welcome", "to", "for":
				continue
			}
			if strings.ContainsAny(word, `\%`) || word[0] >= '0' && word[0] <= '9' {
				break
			}
			return word
		}
	}
	return ""
}

func parseImageProbe(out string) imageIdentity {
	sections := map[string][]string{}
	current := ""
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		line = strings.TrimSpace(line)
		if path, ok := strings.CutPrefix(line, "##"); ok {
			current = path
			continue
		}
		if line != "" && current != "" {
			sections[current] = append(sections[current], line)
		}
	}
	values := map[string]string{}
	for _, line := range sections["/etc/image-version"] {
		if key, value, ok := strings.Cut(line, "="); ok {
			values[strings.ToLower(strings.TrimSpace(key))] = strings.TrimSpace(value)
		}
	}
	id := imageIdentity{Name: values["distro"], Version: values["version"], Build: values["build"], Box: values["box_type"]}
	if id.Name == "" {
		id.Name = issueName(sections["/etc/issue"])
	}
	for _, path := range []string{"/proc/stb/info/boxtype", "/proc/stb/info/model"} {
		if id.Box != "" {
			break
		}
		if lines := sections[path]; len(lines) > 0 {
			id.Box = lines[0]
		}
	}
	return id
}

func (s *Server) readImageIdentity(ctx context.Context, profile config.Profile) imageIdentity {
	out, _, err := shellRunner{profile}.run(ctx, 8*time.Second, imageProbeCommand, 4<<10)
	if err != nil {
		return imageIdentity{}
	}
	return parseImageProbe(out)
}

func backupArchiveName(id imageIdentity, host string, at time.Time) string {
	name := namePart(id.Name, 32)
	parts := []string{name}
	if name == "" {
		parts[0] = "Receiver"
	}
	if v := namePart(id.Version, 16); v != "" {
		parts = append(parts, "v"+v)
	}
	if b := namePart(id.Build, 16); b != "" {
		parts = append(parts, "b"+b)
	}
	if box := namePart(id.Box, 24); box != "" {
		parts = append(parts, box)
	}
	if len(parts) == 1 && name == "" {
		parts = append(parts, backupHostLabel(host))
	}
	return strings.Join(append(parts, at.Format("2006-01-02_15-04")), "_") + ".tar.gz"
}
