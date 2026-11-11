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
var hostnamePattern = regexp.MustCompile(`^[a-zA-Z0-9.-]+$`)

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
}

func portOr(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func (p Profile) EffectiveFTPPort() int    { return portOr(p.FTPPort, DefaultFTPPort) }
func (p Profile) EffectiveTelnetPort() int { return portOr(p.TelnetPort, DefaultTelnetPort) }

func validPortOrZero(port int) bool { return port == 0 || (port >= 1 && port <= 65535) }

func (p Profile) Validate() error {
	p.Name = strings.TrimSpace(p.Name)
	p.Host = strings.TrimSpace(p.Host)
	if len(p.Name) == 0 || len(p.Name) > 64 {
		return errors.New("name must contain 1 to 64 characters")
	}
	if len(p.Host) == 0 || len(p.Host) > 253 || strings.HasPrefix(p.Host, "-") || strings.ContainsAny(p.Host, "/\\\r\n\t @") {
		return errors.New("host must be an IP address or hostname without a port or path")
	}
	if net.ParseIP(strings.Trim(p.Host, "[]")) == nil && !hostnamePattern.MatchString(p.Host) {
		return errors.New("host contains unsupported characters")
	}
	for label, port := range map[string]int{"FTP port": p.FTPPort, "Telnet port": p.TelnetPort, "web port": p.Port} {
		if !validPortOrZero(port) {
			return fmt.Errorf("%s must be between 1 and 65535", label)
		}
	}
	if len(p.Username) > 128 || strings.ContainsAny(p.Username, "\r\n\x00") {
		return errors.New("username must not exceed 128 characters or contain control characters")
	}
	if len(p.Password) > 4096 || strings.ContainsAny(p.Password, "\r\n\x00") {
		return errors.New("password must not exceed 4096 bytes or contain line breaks/control nulls")
	}
	if p.ID != "" && !idPattern.MatchString(p.ID) {
		return fmt.Errorf("invalid profile id")
	}
	return nil
}
