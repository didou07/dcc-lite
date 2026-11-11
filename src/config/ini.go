package config

import (
	"bufio"
	"bytes"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

const IniFileName = "dcc-lite.ini"

func iniValue(v string) string {
	if v == "" {
		return ""
	}
	if v != strings.TrimSpace(v) || strings.HasPrefix(v, `"`) || strings.ContainsAny(v, "\r\n\x00") {
		return strconv.Quote(v)
	}
	return v
}

func iniParseValue(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && strings.HasPrefix(v, `"`) && strings.HasSuffix(v, `"`) {
		if unquoted, err := strconv.Unquote(v); err == nil {
			return unquoted
		}
	}
	return v
}

func iniBool(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func iniParseBool(v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "true", "1", "on":
		return true, nil
	case "no", "false", "0", "off", "":
		return false, nil
	}
	return false, fmt.Errorf("%q is not yes or no", v)
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
	line("[Remote]")
	line("Enabled=%s", iniBool(r.Enabled))
	line("Listen=%s", iniValue(r.EffectiveListen()))
	if r.TLSHosts != "" {
		line("TLSHosts=%s", iniValue(r.TLSHosts))
	}
	if r.PlainHTTP {
		line("PlainHTTP=yes")
	}
	if r.PasswordHash != "" {
		line("PasswordSalt=%s", iniValue(r.PasswordSalt))
		line("PasswordHash=%s", iniValue(r.PasswordHash))
		line("PasswordIterations=%d", r.PasswordIterations)
	}
	for i, p := range doc.Profiles {
		line("[Device%d]", i+1)
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
		if !p.Enabled {
			line("Enabled=no")
		}
		line("ID=%s", iniValue(p.ID))
	}
	return b.Bytes()
}

func decodeINI(data []byte) (Document, error) {
	var doc Document
	var current *Profile
	inRemote := false
	flush := func() {
		if current != nil {
			doc.Profiles = append(doc.Profiles, *current)
			current = nil
		}
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), maxStoreBytes)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(strings.TrimPrefix(scanner.Text(), "\ufeff"))
		if line == "" || line[0] == ';' || line[0] == '#' {
			continue
		}
		if line[0] == '[' {
			if !strings.HasSuffix(line, "]") {
				return doc, fmt.Errorf("line %d: section name is missing the closing ]", lineNo)
			}
			flush()
			inRemote = false
			name := strings.ToLower(strings.TrimSpace(line[1 : len(line)-1]))
			switch {
			case strings.HasPrefix(name, "device"):
				current = &Profile{Enabled: true}
			case name == "remote":
				inRemote = true
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return doc, fmt.Errorf("line %d: expected Key=Value", lineNo)
		}
		key := strings.ToLower(strings.TrimSpace(line[:eq]))
		value := iniParseValue(line[eq+1:])
		var err error
		switch {
		case inRemote:
			err = decodeRemoteKey(&doc.Remote, key, value)
		case current != nil:
			err = decodeDeviceKey(current, key, value)
		}
		if err != nil {
			return doc, fmt.Errorf("line %d: %s: %w", lineNo, strings.TrimSpace(line[:eq]), err)
		}
	}
	if err := scanner.Err(); err != nil {
		return doc, fmt.Errorf("read settings file: %w", err)
	}
	flush()
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
	case "plainhttp":
		r.PlainHTTP, err = iniParseBool(value)
	case "passwordsalt":
		r.PasswordSalt = value
	case "passwordhash":
		r.PasswordHash = value
	case "passworditerations":
		r.PasswordIterations, err = iniParseInt(value)
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
	}
	return err
}

func iniParseInt(v string) (int, error) {
	if strings.TrimSpace(v) == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, fmt.Errorf("%q is not a number", v)
	}
	return n, nil
}
