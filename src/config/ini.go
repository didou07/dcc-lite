package config

import (
	"bufio"
	"bytes"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

const (
	IniFileName      = "dcc-lite.ini"
	IniFormatVersion = 2
)

func iniValue(v string) string {
	if v == "" {
		return ""
	}
	if v != strings.TrimSpace(v) || strings.ContainsAny(v, "\"\r\n\x00") {
		return strconv.Quote(v)
	}
	return v
}

func iniParseValue(v string) (string, error) {
	v = strings.TrimSpace(v)
	if strings.HasPrefix(v, `"`) || strings.HasSuffix(v, `"`) {
		if len(v) < 2 || !strings.HasPrefix(v, `"`) || !strings.HasSuffix(v, `"`) {
			return "", fmt.Errorf("malformed quoted value")
		}
		unquoted, err := strconv.Unquote(v)
		if err != nil {
			return "", fmt.Errorf("malformed quoted value: %w", err)
		}
		return unquoted, nil
	}
	return v, nil
}

func iniBool(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func iniParseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return false, fmt.Errorf("%q is not yes or no", v)
	}
}

type Document struct {
	Profiles []Profile
	Remote   Remote
}

func encodeINI(doc Document) []byte {
	nl := "\n"
	if runtime.GOOS == "windows" {
		nl = "\r\n"
	}
	var b bytes.Buffer
	line := func(format string, args ...any) {
		fmt.Fprintf(&b, format, args...)
		b.WriteString(nl)
	}
	r := doc.Remote
	line("[Meta]")
	line("Format=%d", IniFormatVersion)
	line("[Remote]")
	line("Enabled=%s", iniBool(r.Enabled))
	line("Listen=%s", iniValue(r.EffectiveListen()))
	if r.TLSHosts != "" {
		line("TLSHosts=%s", iniValue(r.TLSHosts))
	}
	if r.TLSEmail != "" {
		line("TLSEmail=%s", iniValue(r.TLSEmail))
	}
	if r.PasswordHash != "" {
		line("PasswordSalt=%s", iniValue(r.PasswordSalt))
		line("PasswordHash=%s", iniValue(r.PasswordHash))
		line("PasswordIterations=%d", r.PasswordIterations)
	}
	for i, p := range doc.Profiles {
		line("[Device%d]", i+1)
		line("ID=%s", iniValue(p.ID))
		line("Name=%s", iniValue(p.Name))
		line("Host=%s", iniValue(p.Host))
		line("Username=%s", iniValue(p.Username))
		line("Password=%s", iniValue(p.Password))
		line("FTPPort=%d", p.EffectiveFTPPort())
		line("TelnetPort=%d", p.EffectiveTelnetPort())
		if p.Port != 0 {
			line("WebPort=%d", p.Port)
		}
		if p.Scheme != "" {
			line("Scheme=%s", iniValue(p.Scheme))
		}
		if p.InsecureTLS {
			line("InsecureTLS=yes")
		}
		line("Enabled=%s", iniBool(p.Enabled))
		if p.Group != "" {
			line("Group=%s", iniValue(p.Group))
		}
		if p.Color != "" {
			line("Color=%s", iniValue(p.Color))
		}
		if p.Favorite {
			line("Favorite=yes")
		}
		if p.StartDir != "" {
			line("StartDir=%s", iniValue(p.StartDir))
		}
		if p.Note != "" {
			line("Note=%s", iniValue(p.Note))
		}
	}
	return b.Bytes()
}

func decodeINI(data []byte) (Document, error) {
	var doc Document
	var current *Profile
	var section string
	sectionKeys := make(map[string]bool)
	seenSections := make(map[string]bool)
	remoteKeys := make(map[string]bool)
	seenFormat, seenRemote := false, false
	nextDevice := 1
	lineNo := 0

	validateSection := func(name string) error {
		switch name {
		case "meta":
			if seenFormat || seenRemote || nextDevice != 1 {
				return fmt.Errorf("[Meta] must be the first and only metadata section")
			}
		case "remote":
			if !seenFormat || seenRemote || current != nil || nextDevice != 1 {
				return fmt.Errorf("[Remote] must follow [Meta] and appear once")
			}
		case "":
			return fmt.Errorf("empty section name")
		default:
			if !strings.HasPrefix(name, "device") || !seenRemote {
				return fmt.Errorf("unknown section [%s]", name)
			}
			n, err := strconv.Atoi(strings.TrimPrefix(name, "device"))
			if err != nil || n < 1 || n != nextDevice || fmt.Sprintf("device%d", n) != name {
				return fmt.Errorf("device sections must be numbered sequentially starting at [Device1]")
			}
			nextDevice++
		}
		return nil
	}

	flush := func() error {
		if current == nil {
			return nil
		}
		for _, key := range []string{"id", "name", "host", "username", "password", "ftpport", "telnetport", "enabled"} {
			if !sectionKeys[key] {
				return fmt.Errorf("[%s] is missing required key %s", section, key)
			}
		}
		doc.Profiles = append(doc.Profiles, *current)
		current = nil
		return nil
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), maxStoreBytes)
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			if !strings.HasSuffix(line, "]") || strings.Count(line, "[") != 1 || strings.Count(line, "]") != 1 {
				return doc, fmt.Errorf("line %d: malformed section header", lineNo)
			}
			if err := flush(); err != nil {
				return doc, fmt.Errorf("line %d: %w", lineNo, err)
			}
			name := strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			if seenSections[name] {
				return doc, fmt.Errorf("line %d: duplicate section [%s]", lineNo, name)
			}
			if err := validateSection(name); err != nil {
				return doc, fmt.Errorf("line %d: %w", lineNo, err)
			}
			seenSections[name] = true
			section, sectionKeys = name, make(map[string]bool)
			if strings.HasPrefix(name, "device") {
				current = &Profile{}
			}
			if name == "remote" {
				seenRemote = true
			}
			continue
		}
		if section == "" {
			return doc, fmt.Errorf("line %d: setting appears before [Meta]", lineNo)
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return doc, fmt.Errorf("line %d: expected Key=Value", lineNo)
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		if key == "" {
			return doc, fmt.Errorf("line %d: empty key", lineNo)
		}
		if sectionKeys[key] {
			return doc, fmt.Errorf("line %d: duplicate key %s", lineNo, key)
		}
		value, err := iniParseValue(line[eq+1:])
		if err != nil {
			return doc, fmt.Errorf("line %d: %s: %w", lineNo, key, err)
		}
		switch {
		case section == "meta":
			if key != "format" {
				return doc, fmt.Errorf("line %d: unknown [Meta] key %s", lineNo, key)
			}
			format, err := iniParseInt(value)
			if err != nil || format != IniFormatVersion {
				return doc, fmt.Errorf("line %d: unsupported settings format %q (expected %d)", lineNo, value, IniFormatVersion)
			}
			seenFormat = true
		case section == "remote":
			err = decodeRemoteKey(&doc.Remote, key, value)
			if err == nil {
				remoteKeys[key] = true
			}
		case strings.HasPrefix(section, "device"):
			err = decodeDeviceKey(current, key, value)
		}
		if err != nil {
			return doc, fmt.Errorf("line %d: %s: %w", lineNo, key, err)
		}
		sectionKeys[key] = true
	}
	if err := scanner.Err(); err != nil {
		return doc, fmt.Errorf("read settings file: %w", err)
	}
	if err := flush(); err != nil {
		return doc, err
	}
	if !seenFormat || !seenSections["meta"] {
		return doc, fmt.Errorf("settings file is missing [Meta] Format=%d", IniFormatVersion)
	}
	if !seenRemote {
		return doc, fmt.Errorf("settings file is missing [Remote]")
	}
	if !remoteKeys["enabled"] || !remoteKeys["listen"] {
		return doc, fmt.Errorf("[Remote] is missing required Enabled or Listen setting")
	}
	passwordFields := 0
	for _, key := range []string{"passwordsalt", "passwordhash", "passworditerations"} {
		if remoteKeys[key] {
			passwordFields++
		}
	}
	if passwordFields != 0 && passwordFields != 3 {
		return doc, fmt.Errorf("[Remote] password record must contain PasswordSalt, PasswordHash and PasswordIterations")
	}
	if passwordFields == 3 && (doc.Remote.PasswordSalt == "" || doc.Remote.PasswordHash == "" || doc.Remote.PasswordIterations <= 0) {
		return doc, fmt.Errorf("[Remote] password record contains empty or invalid values")
	}
	return doc, nil
}

func decodeRemoteKey(r *Remote, key, value string) error {
	var err error
	switch key {
	case "enabled":
		r.Enabled, err = iniParseBool(value)
	case "listen":
		r.Listen = value
	case "tlshosts":
		r.TLSHosts = value
	case "tlsemail":
		r.TLSEmail = value
	case "passwordsalt":
		r.PasswordSalt = value
	case "passwordhash":
		r.PasswordHash = value
	case "passworditerations":
		r.PasswordIterations, err = iniParseInt(value)
	default:
		return fmt.Errorf("unknown [Remote] key %s", key)
	}
	return err
}

func decodeDeviceKey(p *Profile, key, value string) error {
	var err error
	switch key {
	case "id":
		p.ID = value
	case "name":
		p.Name = value
	case "host":
		p.Host = value
	case "scheme":
		p.Scheme = strings.ToLower(value)
	case "webport":
		p.Port, err = iniParseInt(value)
	case "ftpport":
		p.FTPPort, err = iniParseInt(value)
	case "telnetport":
		p.TelnetPort, err = iniParseInt(value)
	case "username":
		p.Username = value
	case "password":
		p.Password = value
	case "enabled":
		p.Enabled, err = iniParseBool(value)
	case "insecuretls":
		p.InsecureTLS, err = iniParseBool(value)
	case "group":
		p.Group = value
	case "color":
		p.Color = strings.ToLower(value)
	case "favorite":
		p.Favorite, err = iniParseBool(value)
	case "startdir":
		p.StartDir = value
	case "note":
		p.Note = value
	default:
		return fmt.Errorf("unknown [%s] key %s", "Device", key)
	}
	return err
}

func iniParseInt(v string) (int, error) {
	if strings.TrimSpace(v) == "" {
		return 0, fmt.Errorf("empty value is not a number")
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", v)
	}
	return n, nil
}
