package terminal

import (
	"bufio"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
)

const (
	telnetIAC  = 255
	telnetDONT = 254
	telnetDO   = 253
	telnetWONT = 252
	telnetWILL = 251
	telnetSB   = 250
	telnetSE   = 240

	telnetOptEcho   = 1
	telnetOptSGA    = 3
	telnetOptTTYPE  = 24
	telnetOptNAWS   = 31
	telnetTTYPEIS   = 0
	telnetTTYPESEND = 1
)

type TelnetSession struct {
	conn           net.Conn
	read           *bufio.Reader
	mu             sync.Mutex
	authMu         sync.Mutex
	cols           uint16
	rows           uint16
	nawsEnabled    bool
	username       string
	password       []byte
	loginStage     uint8
	promptTail     string
	initialCommand []byte
	commandSent    bool
}

func NewTelnetSession(conn net.Conn, columns, rows int, credentials ...string) *TelnetSession {
	if columns < 1 || columns > 65535 {
		columns = 100
	}
	if rows < 1 || rows > 65535 {
		rows = 30
	}
	session := &TelnetSession{conn: conn, read: bufio.NewReaderSize(conn, 4096), cols: uint16(columns), rows: uint16(rows)}
	if len(credentials) > 0 {
		session.username = credentials[0]
	}
	if len(credentials) > 1 {
		session.password = []byte(credentials[1])
	}
	return session
}

func (s *TelnetSession) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for {
		b, err := s.read.ReadByte()
		if err != nil {
			return 0, err
		}
		if b != telnetIAC {
			p[0] = b
			n := 1
			for n < len(p) && s.read.Buffered() > 0 {
				peek, _ := s.read.Peek(1)
				if len(peek) == 0 || peek[0] == telnetIAC {
					break
				}
				p[n], _ = s.read.ReadByte()
				n++
			}
			s.processLoginPrompt(p[:n])
			return n, nil
		}
		command, err := s.read.ReadByte()
		if err != nil {
			return 0, err
		}
		switch command {
		case telnetIAC:
			p[0] = telnetIAC
			return 1, nil
		case telnetWILL, telnetWONT, telnetDO, telnetDONT:
			option, readErr := s.read.ReadByte()
			if readErr != nil {
				return 0, readErr
			}
			if err := s.negotiate(command, option); err != nil {
				return 0, err
			}
		case telnetSB:
			if err := s.subnegotiation(); err != nil {
				return 0, err
			}
		default:
			// Other Telnet command bytes do not carry payload.
		}
	}
}

func (s *TelnetSession) SetInitialCommand(command []byte) error {
	if len(command) == 0 {
		return nil
	}
	if len(command) > 8192 || !strings.HasSuffix(string(command), "\r") || strings.ContainsAny(string(command[:len(command)-1]), "\r\n\x00") {
		return errors.New("invalid initial terminal command")
	}
	s.authMu.Lock()
	defer s.authMu.Unlock()
	if s.commandSent {
		return errors.New("terminal command was already sent")
	}
	for i := range s.initialCommand {
		s.initialCommand[i] = 0
	}
	s.initialCommand = append([]byte(nil), command...)
	return nil
}

func (s *TelnetSession) processLoginPrompt(data []byte) {
	s.authMu.Lock()
	text := strings.ToLower(s.promptTail + string(data))
	if len(text) > 96 {
		s.promptTail = text[len(text)-96:]
	} else {
		s.promptTail = text
	}

	var response []byte
	if s.username != "" && s.loginStage < 2 {
		if s.loginStage == 0 && (strings.Contains(text, "login:") || strings.Contains(text, "username:") || strings.Contains(text, "user name:") || strings.Contains(text, "user:")) {
			s.loginStage = 1
			response = append([]byte(s.username), '\r', '\n')
		}
		if s.loginStage == 1 && len(s.password) > 0 && (strings.Contains(text, "password:") || strings.Contains(text, "passwd:") || strings.Contains(text, "pass:")) {
			s.loginStage = 2
			response = append(response, s.password...)
			response = append(response, '\r', '\n')
			for i := range s.password {
				s.password[i] = 0
			}
			s.password = nil
		}
	}
	if !s.commandSent && len(s.initialCommand) > 0 && looksLikeShellPrompt(text) {
		s.commandSent = true
		response = append(response, s.initialCommand...)
		for i := range s.initialCommand {
			s.initialCommand[i] = 0
		}
		s.initialCommand = nil
	}
	s.authMu.Unlock()
	if len(response) > 0 {
		_, _ = s.Write(response)
		for i := range response {
			response[i] = 0
		}
	}
}

func looksLikeShellPrompt(value string) bool {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\r", ""))
	if value == "" {
		return false
	}
	if index := strings.LastIndex(value, "\n"); index >= 0 {
		value = strings.TrimSpace(value[index+1:])
	}
	return strings.HasSuffix(value, "#") || strings.HasSuffix(value, "$") || strings.HasSuffix(value, ">")
}

func (s *TelnetSession) negotiate(command, option byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch command {
	case telnetWILL:
		if option == telnetOptEcho || option == telnetOptSGA || option == telnetOptTTYPE {
			return s.writeCommandLocked(telnetDO, option)
		}
		return s.writeCommandLocked(telnetDONT, option)
	case telnetWONT:
		return s.writeCommandLocked(telnetDONT, option)
	case telnetDO:
		if option == telnetOptSGA || option == telnetOptTTYPE || option == telnetOptNAWS {
			if err := s.writeCommandLocked(telnetWILL, option); err != nil {
				return err
			}
			if option == telnetOptNAWS {
				s.nawsEnabled = true
				return s.writeNAWSLocked()
			}
			return nil
		}
		return s.writeCommandLocked(telnetWONT, option)
	case telnetDONT:
		if option == telnetOptNAWS || option == telnetOptTTYPE || option == telnetOptSGA {
			return s.writeCommandLocked(telnetWONT, option)
		}
	}
	return nil
}

func (s *TelnetSession) subnegotiation() error {
	option, err := s.read.ReadByte()
	if err != nil {
		return err
	}
	payload := make([]byte, 0, 16)
	for len(payload) < 4096 {
		b, readErr := s.read.ReadByte()
		if readErr != nil {
			return readErr
		}
		if b == telnetIAC {
			next, readErr := s.read.ReadByte()
			if readErr != nil {
				return readErr
			}
			if next == telnetSE {
				break
			}
			if next == telnetIAC {
				payload = append(payload, telnetIAC)
				continue
			}
			payload = append(payload, telnetIAC, next)
			continue
		}
		payload = append(payload, b)
	}
	if len(payload) >= 4096 {
		return errors.New("Telnet subnegotiation exceeded size limit")
	}
	if option == telnetOptTTYPE && len(payload) > 0 && payload[0] == telnetTTYPESEND {
		s.mu.Lock()
		defer s.mu.Unlock()
		return s.writeSubnegotiationLocked(telnetOptTTYPE, append([]byte{telnetTTYPEIS}, []byte("xterm-256color")...))
	}
	return nil
}

func (s *TelnetSession) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	encoded := make([]byte, 0, len(p)+8)
	for _, b := range p {
		encoded = append(encoded, b)
		if b == telnetIAC {
			encoded = append(encoded, telnetIAC)
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := writeAll(s.conn, encoded); err != nil {
		return 0, err
	}
	return len(p), nil
}

func (s *TelnetSession) Resize(columns, rows int) error {
	if columns < 1 || rows < 1 || columns > 65535 || rows > 65535 {
		return errors.New("terminal dimensions are out of range")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cols, s.rows = uint16(columns), uint16(rows)
	if !s.nawsEnabled {
		return nil
	}
	return s.writeNAWSLocked()
}

func (s *TelnetSession) writeNAWSLocked() error {
	payload := make([]byte, 4)
	binary.BigEndian.PutUint16(payload[:2], s.cols)
	binary.BigEndian.PutUint16(payload[2:], s.rows)
	return s.writeSubnegotiationLocked(telnetOptNAWS, payload)
}

func (s *TelnetSession) writeSubnegotiationLocked(option byte, payload []byte) error {
	encoded := []byte{telnetIAC, telnetSB, option}
	for _, b := range payload {
		encoded = append(encoded, b)
		if b == telnetIAC {
			encoded = append(encoded, telnetIAC)
		}
	}
	encoded = append(encoded, telnetIAC, telnetSE)
	return writeAll(s.conn, encoded)
}

func (s *TelnetSession) writeCommandLocked(command, option byte) error {
	return writeAll(s.conn, []byte{telnetIAC, command, option})
}

func (s *TelnetSession) Close() error {
	s.authMu.Lock()
	for i := range s.password {
		s.password[i] = 0
	}
	s.password = nil
	for i := range s.initialCommand {
		s.initialCommand[i] = 0
	}
	s.initialCommand = nil
	s.authMu.Unlock()
	return s.conn.Close()
}

func writeAll(w io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := w.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
