package config

import (
	"errors"
	"fmt"
	"net"
	"regexp"
	"strings"
)

const (
	DefaultUsername = "root"
	DefaultPassword = "dreambox"
)

var idPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{8,64}$`)

const (
	DefaultFTPPort    = 21
	DefaultTelnetPort = 23
)

type Profile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Host        string `json:"host"`
	Port        int    `json:"port,omitempty"`
	Scheme      string `json:"scheme,omitempty"`
	FTPPort     int    `json:"ftp_port,omitempty"`
	TelnetPort  int    `json:"telnet_port,omitempty"`
	Username    string `json:"username,omitempty"`
	Password    string `json:"-"`
	Enabled     bool   `json:"enabled"`
	InsecureTLS bool   `json:"tls_insecure_skip_verify,omitempty"`

	Group    string `json:"group,omitempty"`     // folder name in the site list
	Color    string `json:"color,omitempty"`     // one of SiteColors, or empty
	Favorite bool   `json:"favorite,omitempty"`  // pinned to the top
	StartDir string `json:"start_dir,omitempty"` // remote folder opened by Files and Terminal
	Note     string `json:"note,omitempty"`      // free text, shown on the card
}

// SiteColors are the colour labels a saved site can carry.
var SiteColors = []string{"red", "orange", "amber", "green", "teal", "blue", "violet", "pink"}

func validSiteColor(c string) bool {
	if c == "" {
		return true
	}
	for _, v := range SiteColors {
		if v == c {
			return true
		}
	}
	return false
}

func hasControl(s string) bool { return strings.ContainsAny(s, "\r\n\x00\t") }

func portOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func (p Profile) EffectiveFTPPort() int    { return portOr(p.FTPPort, DefaultFTPPort) }
func (p Profile) EffectiveTelnetPort() int { return portOr(p.TelnetPort, DefaultTelnetPort) }
func (p Profile) EffectiveWebPort() int {
	fallback := 80
	if p.Scheme == "https" {
		fallback = 443
	}
	port := p.Port
	if port == 80 && p.Scheme == "https" {
		port = 0
	} else if port == 443 && p.Scheme != "https" {
		port = 0
	}
	return portOr(port, fallback)
}

func validPortOrZero(port int) bool { return port == 0 || (port >= 1 && port <= 65535) }

func validDeviceHost(host string) bool {
	host = strings.Trim(host, "[]")
	if net.ParseIP(host) != nil {
		return true
	}
	host = strings.TrimSuffix(host, ".")
	if len(host) == 0 || len(host) > 253 || strings.Contains(host, "..") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, r := range label {
			if !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(r >= '0' && r <= '9') && r != '-' {
				return false
			}
		}
	}
	return true
}

func (p Profile) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	p.Host = strings.TrimSpace(p.Host)
	if len(p.Name) == 0 || len(p.Name) > 64 || strings.ContainsAny(p.Name, "\r\n\x00") {
		return errors.New("name must contain 1 to 64 characters without control characters")
	}
	if len(p.Host) == 0 || len(p.Host) > 253 || strings.ContainsAny(p.Host, "/\\\r\n\t @") || !validDeviceHost(p.Host) {
		return errors.New("host must be a valid IP address or DNS hostname without a port or path")
	}
	for label, port := range map[string]int{"FTP port": p.FTPPort, "Telnet port": p.TelnetPort, "web port": p.Port} {
		if !validPortOrZero(port) {
			return fmt.Errorf("%s must be between 1 and 65535", label)
		}
	}
	if p.Scheme != "" && p.Scheme != "http" && p.Scheme != "https" {
		return errors.New("web scheme must be http or https")
	}
	if p.InsecureTLS && p.Scheme != "https" {
		return errors.New("accepting an untrusted TLS certificate requires HTTPS")
	}
	if len(p.Group) > 40 || hasControl(p.Group) {
		return errors.New("group must be at most 40 characters without control characters")
	}
	if !validSiteColor(p.Color) {
		return errors.New("unknown colour label")
	}
	if len(p.StartDir) > 512 || hasControl(p.StartDir) || (p.StartDir != "" && !strings.HasPrefix(p.StartDir, "/")) {
		return errors.New("start folder must be an absolute path such as /etc/enigma2")
	}
	if len(p.Note) > 200 || strings.ContainsAny(p.Note, "\r\n\x00") {
		return errors.New("note must be at most 200 characters on one line")
	}
	if p.Username == "" || len(p.Username) > 128 || strings.ContainsAny(p.Username, "\r\n\x00") {
		return errors.New("username must contain 1 to 128 characters without control characters")
	}
	if len(p.Password) > 4096 || strings.ContainsAny(p.Password, "\r\n\x00") {
		return errors.New("password must not exceed 4096 bytes or contain line breaks/control nulls")
	}
	if !idPattern.MatchString(p.ID) {
		return errors.New("invalid or missing profile id")
	}
	return nil
}
