package enigma2

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var fileNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,100}\.(tv|radio)$`)

func ValidBouquetFile(name string) bool {
	return fileNamePattern.MatchString(name) && !strings.Contains(name, "..")
}

func KindOf(file string) string {
	if strings.HasSuffix(file, ".radio") {
		return "radio"
	}
	return "tv"
}

type Entry struct {
	Ref            string `json:"ref"`
	Description    string `json:"description"`
	HasDescription bool   `json:"-"`
}

func (e Entry) IsMarker() bool {
	fields := strings.Split(e.Ref, ":")
	if len(fields) < 3 {
		return false
	}
	flags, err := strconv.Atoi(fields[1])
	return err == nil && flags&64 != 0
}

type Bouquet struct {
	Name string

	Header  []string
	Entries []Entry
}

const MaxEntries = 20000

func ParseBouquet(text string) (Bouquet, error) {
	var b Bouquet
	var last *Entry
	for _, raw := range splitLines(text) {
		line := strings.TrimRight(raw, "\r")
		if ref, ok := directive(line, "#SERVICE"); ok {
			if len(b.Entries) >= MaxEntries {
				return Bouquet{}, errors.New("the bouquet has too many entries")
			}
			b.Entries = append(b.Entries, Entry{Ref: strings.TrimSpace(ref)})
			last = &b.Entries[len(b.Entries)-1]
			continue
		}
		if desc, ok := directive(line, "#DESCRIPTION"); ok {
			if last != nil && !last.HasDescription {
				last.Description = strings.TrimRight(desc, " ")
				last.HasDescription = true
			} else if last == nil {
				b.Header = append(b.Header, line)
			}
			continue
		}
		switch {
		case strings.HasPrefix(line, "#NAME "):
			b.Name = strings.TrimSpace(line[len("#NAME "):])
		case line == "":
		default:
			if last == nil && strings.HasPrefix(line, "#") {
				b.Header = append(b.Header, line)
			}
		}
	}
	if b.Name == "" && len(b.Entries) == 0 && len(b.Header) == 0 {
		return Bouquet{}, errors.New("this does not look like a bouquet file (no #NAME or #SERVICE lines)")
	}
	return b, nil
}

func (b Bouquet) Format() string {
	var out strings.Builder
	out.WriteString("#NAME " + oneLine(b.Name) + "\n")
	for _, line := range b.Header {
		out.WriteString(oneLine(line) + "\n")
	}
	for _, e := range b.Entries {
		out.WriteString("#SERVICE " + oneLine(e.Ref) + "\n")
		if e.HasDescription || e.Description != "" {
			out.WriteString("#DESCRIPTION " + oneLine(e.Description) + "\n")
		}
	}
	return out.String()
}

func (b Bouquet) NewMarker(title string) Entry {
	next := 1
	for _, e := range b.Entries {
		if !e.IsMarker() {
			continue
		}

		if fields := strings.Split(e.Ref, ":"); len(fields) > 2 {
			if n, err := strconv.ParseUint(fields[2], 16, 32); err == nil && int(n) >= next {
				next = int(n) + 1
			}
		}
	}
	return Entry{Ref: fmt.Sprintf("1:64:%X:0:0:0:0:0:0:0:", next), Description: oneLine(title), HasDescription: true}
}

func ValidateEntry(e Entry) error {
	if strings.TrimSpace(e.Ref) == "" || len(e.Ref) > 1024 || strings.ContainsAny(e.Ref, "\r\n\x00") {
		return errors.New("an entry has an invalid service reference")
	}
	if len(e.Description) > 512 || strings.ContainsAny(e.Description, "\r\n\x00") {
		return errors.New("an entry has an invalid description")
	}
	if strings.Count(e.Ref, ":") < 3 {
		return fmt.Errorf("%q is not a service reference", e.Ref)
	}
	return nil
}

func directive(line, keyword string) (string, bool) {
	if !strings.HasPrefix(line, keyword) {
		return "", false
	}
	rest := line[len(keyword):]
	switch {
	case strings.HasPrefix(rest, ":"):
		rest = rest[1:]
	case strings.HasPrefix(rest, " "):
	default:
		return "", false
	}
	return strings.TrimPrefix(rest, " "), true
}

func oneLine(s string) string {
	return strings.NewReplacer("\r", " ", "\n", " ", "\x00", "").Replace(s)
}

func splitLines(text string) []string {
	text = strings.TrimPrefix(text, "\ufeff")
	return strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
}

type IndexLine struct {
	Raw  string
	File string
}

type Index struct {
	Name  string
	Lines []IndexLine
}

var fromBouquet = regexp.MustCompile(`FROM BOUQUET "([^"]+)"`)

func ParseIndex(text string) (Index, error) {
	var idx Index
	for _, raw := range splitLines(text) {
		line := strings.TrimRight(raw, "\r")
		if ref, ok := directive(line, "#SERVICE"); ok {
			l := IndexLine{Raw: "#SERVICE " + strings.TrimSpace(ref)}
			if m := fromBouquet.FindStringSubmatch(ref); m != nil {
				l.File = m[1]
			}
			idx.Lines = append(idx.Lines, l)
			continue
		}
		if strings.HasPrefix(line, "#NAME ") {
			idx.Name = strings.TrimSpace(line[len("#NAME "):])
		}
	}
	if idx.Name == "" && len(idx.Lines) == 0 {
		return Index{}, errors.New("this does not look like a bouquet index (no #NAME or #SERVICE lines)")
	}
	return idx, nil
}

func (i Index) Files() []string {
	var out []string
	for _, l := range i.Lines {
		if l.File != "" && ValidBouquetFile(l.File) {
			out = append(out, l.File)
		}
	}
	return out
}

func (i Index) Has(file string) bool {
	for _, l := range i.Lines {
		if l.File == file {
			return true
		}
	}
	return false
}

func (i *Index) Add(file string) {
	slot := "1"
	if KindOf(file) == "radio" {
		slot = "2"
	}
	i.Lines = append(i.Lines, IndexLine{
		Raw:  fmt.Sprintf(`#SERVICE 1:7:%s:0:0:0:0:0:0:0:FROM BOUQUET "%s" ORDER BY bouquet`, slot, file),
		File: file,
	})
}

func (i *Index) Remove(file string) bool {
	kept := i.Lines[:0:0]
	removed := false
	for _, l := range i.Lines {
		if l.File == file {
			removed = true
			continue
		}
		kept = append(kept, l)
	}
	i.Lines = kept
	return removed
}

func (i *Index) Move(file string, delta int) bool {
	from := -1
	for n, l := range i.Lines {
		if l.File == file {
			from = n
			break
		}
	}
	to := from + delta
	if from < 0 || to < 0 || to >= len(i.Lines) || delta == 0 {
		return false
	}
	i.Lines[from], i.Lines[to] = i.Lines[to], i.Lines[from]
	return true
}

func (i Index) Format() string {
	var out strings.Builder
	out.WriteString("#NAME " + oneLine(i.Name) + "\n")
	for _, l := range i.Lines {
		out.WriteString(oneLine(l.Raw) + "\n")
	}
	return out.String()
}
