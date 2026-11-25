package logx

import (
	"fmt"
	"net"
	"os"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Term struct {
	Value string
	Kind  string
}

type Sanitizer struct {
	rules  []termRule
	labels map[string]int
	counts map[string]int
}

type termRule struct {
	re   *regexp.Regexp
	kind string
	key  string
}

var (
	reIPv4      = regexp.MustCompile(`\b(?:(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\.){3}(?:25[0-5]|2[0-4]\d|1\d\d|[1-9]?\d)\b`)
	reIPv6      = regexp.MustCompile(`[0-9A-Fa-f:.]*:[0-9A-Fa-f:.]*`)
	reMAC       = regexp.MustCompile(`(?i)\b[0-9a-f]{2}(?:[:-][0-9a-f]{2}){5}\b`)
	reEmail     = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9\-]+(?:\.[A-Za-z0-9\-]+)*\.[A-Za-z]{2,}`)
	reUserPath  = regexp.MustCompile(`(?i)(\\Users\\|/Users/|/home/)([^\\/\s"'=]+)`)
	systemPaths = []string{"/etc", "/usr", "/var", "/tmp", "/proc", "/sys", "/lib", "/bin", "/sbin", "/boot", "/dev", "/run", "/opt"}
)

var genericDirs = map[string]bool{
	"movie": true, "movies": true, "music": true, "picture": true, "pictures": true, "video": true, "videos": true,
	"backup": true, "backups": true, "epg": true, "timeshift": true, "picon": true, "ipk": true, "tmp": true,
	"enigma2": true, "keys": true, "plugins": true, "hdd": true, "usb": true, "net": true, "ba": true,
}

func NewSanitizer(terms ...Term) *Sanitizer {
	s := &Sanitizer{labels: map[string]int{}, counts: map[string]int{}}
	if host, err := os.Hostname(); err == nil {
		terms = append(terms, Term{Value: host, Kind: "computer"})
	}
	seen := map[string]bool{}
	for _, t := range terms {
		v := strings.TrimSpace(t.Value)
		lower := strings.ToLower(v)
		if utf8.RuneCountInString(v) < 3 || seen[lower] || net.ParseIP(strings.Trim(v, "[]")) != nil {
			continue
		}
		switch lower {
		case "root", "localhost", "admin", "enigma2", "dreambox":
			continue
		}
		seen[lower] = true
		pattern := regexp.QuoteMeta(v)
		if r, _ := utf8.DecodeRuneInString(v); isASCIIWord(r) {
			pattern = `\b` + pattern
		}
		if r, _ := utf8.DecodeLastRuneInString(v); isASCIIWord(r) {
			pattern += `\b`
		}
		s.rules = append(s.rules, termRule{re: regexp.MustCompile(`(?i)` + pattern), kind: t.Kind, key: lower})
	}
	sort.SliceStable(s.rules, func(i, j int) bool { return len(s.rules[i].key) > len(s.rules[j].key) })
	return s
}

func isASCIIWord(r rune) bool {
	return r < unicode.MaxASCII && (r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r))
}

func (s *Sanitizer) label(kind, value string) string {
	key := kind + "\x00" + strings.ToLower(value)
	n, ok := s.labels[key]
	if !ok {
		s.counts[kind]++
		n = s.counts[kind]
		s.labels[key] = n
	}
	return fmt.Sprintf("%s#%d", kind, n)
}

func keepIP(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsUnspecified() || ip.IsMulticast() || ip.Equal(net.IPv4bcast)
}

// Text masks personal data in free text.
func (s *Sanitizer) Text(text string) string {
	if text == "" {
		return text
	}
	text = Redact(text)
	for _, r := range s.rules {
		kind, key := r.kind, r.key
		text = r.re.ReplaceAllStringFunc(text, func(string) string { return s.label(kind, key) })
	}
	text = reEmail.ReplaceAllStringFunc(text, func(m string) string { return s.label("email", m) })
	text = reMAC.ReplaceAllStringFunc(text, func(m string) string { return s.label("mac", m) })
	text = reIPv4.ReplaceAllStringFunc(text, func(m string) string {
		if keepIP(net.ParseIP(m)) {
			return m
		}
		return s.label("ip", m)
	})
	text = reIPv6.ReplaceAllStringFunc(text, func(m string) string {
		core := strings.TrimRight(m, ":.")
		tail := m[len(core):]
		ip := net.ParseIP(core)
		if ip == nil || !strings.Contains(core, ":") || keepIP(ip) {
			return m
		}
		return s.label("ip", core) + tail
	})
	text = reUserPath.ReplaceAllStringFunc(text, func(m string) string {
		parts := reUserPath.FindStringSubmatch(m)
		if strings.EqualFold(parts[2], "root") {
			return m
		}
		return parts[1] + "user"
	})
	return text
}

func (s *Sanitizer) Path(p string) string {
	if !strings.HasPrefix(p, "/") {
		return p
	}
	for _, prefix := range systemPaths {
		if p == prefix || strings.HasPrefix(p, prefix+"/") {
			return p
		}
	}
	segments := strings.Split(strings.Trim(p, "/"), "/")
	for i, seg := range segments {
		ext := path.Ext(seg)
		if len(ext) > 6 || strings.ContainsAny(ext, " ") {
			ext = ""
		}
		last := i == len(segments)-1
		switch {
		case i < 2 || genericDirs[strings.ToLower(seg)]:
			continue
		case last && ext != "":
			segments[i] = s.label("file", seg) + ext
		default:
			segments[i] = s.label("folder", seg)
		}
	}
	out := "/" + strings.Join(segments, "/")
	if strings.HasSuffix(p, "/") {
		out += "/"
	}
	return out
}

func (s *Sanitizer) field(f Field) Field {
	if isSensitiveKey(f.Key) {
		return Field{Key: f.Key, Value: "***"}
	}
	v := f.Value
	if f.Key == "remote_path" || f.Key == "path_on_device" {
		v = s.Path(v)
	}
	return Field{Key: f.Key, Value: s.Text(v)}
}

func (s *Sanitizer) fields(in []Field) []Field {
	if in == nil {
		return nil
	}
	out := make([]Field, len(in))
	for i, f := range in {
		out[i] = s.field(f)
	}
	return out
}

func (s *Sanitizer) entry(e Entry) Entry {
	e.Message = s.Text(e.Message)
	e.Fields = s.fields(e.Fields)
	e.Stack = s.Text(e.Stack)
	return e
}

func (s *Sanitizer) fault(f Fault) Fault {
	f.Message = s.Text(f.Message)
	f.Error = s.Text(f.Error)
	f.Route = s.Text(f.Route)
	f.Stack = s.Text(f.Stack)
	f.Context = s.fields(f.Context)
	return f
}
