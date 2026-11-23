// Package ftptest is a tiny in-memory FTP server for tests. It is only imported
// from _test.go files, so it never ends up in the program.
package ftptest

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Server is a tiny in-memory FTP server, just enough to exercise the client.
type Server struct {
	t        testing.TB
	ln       net.Listener
	mu       sync.Mutex
	files    map[string][]byte
	dirs     map[string]bool
	NoMLSD   bool // answer MLSD with 500 so the client falls back to LIST
	NoEPSV   bool // answer EPSV with 502 so the client falls back to PASV
	Password string
	conns    []net.Conn
}

func Start(t testing.TB) *Server {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &Server{t: t, ln: ln, files: map[string][]byte{}, dirs: map[string]bool{"/": true}, Password: "secret"}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.conns = append(s.conns, c)
			s.mu.Unlock()
			go s.serve(c)
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		s.DropAll()
	})
	return s
}

// Port is the TCP port the server listens on.
func (s *Server) Port() int { return s.ln.Addr().(*net.TCPAddr).Port }

// DropAll closes every open control connection.
func (s *Server) DropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.Close()
	}
	s.conns = nil
}

// Put stores a file (absolute path) on the server.
func (s *Server) Put(name string, data []byte) {
	s.mu.Lock()
	s.files[name] = append([]byte(nil), data...)
	s.mu.Unlock()
}

// Get returns a stored file.
func (s *Server) Get(name string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.files[name]
	return append([]byte(nil), d...), ok
}

func (s *Server) listing(dir string, mlsd bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	dir = path.Clean("/" + dir)
	var lines []string
	for name, data := range s.files {
		if path.Dir(name) == dir {
			if mlsd {
				lines = append(lines, fmt.Sprintf("type=file;size=%d;modify=20240102030405;unix.mode=0644; %s", len(data), path.Base(name)))
			} else {
				lines = append(lines, fmt.Sprintf("-rw-r--r-- 1 root root %d Jan  2 2024 %s", len(data), path.Base(name)))
			}
		}
	}
	for d := range s.dirs {
		if d != "/" && path.Dir(d) == dir {
			if mlsd {
				lines = append(lines, "type=dir;modify=20240102030405; "+path.Base(d))
			} else {
				lines = append(lines, "drwxr-xr-x 2 root root 4096 Jan  2 2024 "+path.Base(d))
			}
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\r\n") + "\r\n"
}

func (s *Server) serve(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	reply := func(format string, args ...any) { fmt.Fprintf(c, format+"\r\n", args...) }
	reply("220 mini")
	var data net.Listener
	var rest int64
	user := ""
	defer func() {
		if data != nil {
			data.Close()
		}
	}()
	openData := func(code int, text string) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			reply("425 no data")
			return
		}
		if data != nil {
			data.Close()
		}
		data = l
		p := l.Addr().(*net.TCPAddr).Port
		if code == 229 {
			reply("229 Entering Extended Passive Mode (|||%d|)", p)
		} else {
			reply("227 Entering Passive Mode (127,0,0,1,%d,%d)", p/256, p%256)
		}
	}
	accept := func() net.Conn {
		if data == nil {
			return nil
		}
		defer func() { data.Close(); data = nil }()
		if tl, ok := data.(*net.TCPListener); ok {
			tl.SetDeadline(time.Now().Add(5 * time.Second))
		}
		dc, err := data.Accept()
		if err != nil {
			return nil
		}
		return dc
	}
	abs := func(p string) string { return path.Clean("/" + p) }
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		cmd, arg, _ := strings.Cut(line, " ")
		cmd = strings.ToUpper(cmd)
		switch cmd {
		case "USER":
			user = arg
			reply("331 password please")
		case "PASS":
			if user != "" && arg == s.Password {
				reply("230 ok")
			} else {
				reply("530 login incorrect")
			}
		case "TYPE":
			reply("200 binary")
		case "NOOP":
			reply("200 ok")
		case "QUIT":
			reply("221 bye")
			return
		case "PWD":
			reply(`257 "/"`)
		case "CWD":
			s.mu.Lock()
			ok := s.dirs[abs(arg)]
			s.mu.Unlock()
			if ok {
				reply("250 ok")
			} else {
				reply("550 no such directory")
			}
		case "EPSV":
			if s.NoEPSV {
				reply("502 not implemented")
			} else {
				openData(229, "")
			}
		case "PASV":
			openData(227, "")
		case "REST":
			rest, _ = strconv.ParseInt(arg, 10, 64)
			reply("350 restarting at %d", rest)
		case "SIZE":
			if d, ok := s.Get(abs(arg)); ok {
				reply("213 %d", len(d))
			} else {
				reply("550 file not found")
			}
		case "MLSD", "LIST":
			if cmd == "MLSD" && s.NoMLSD {
				reply("500 unknown command")
				continue
			}
			s.mu.Lock()
			ok := s.dirs[abs(arg)]
			s.mu.Unlock()
			if !ok {
				if data != nil {
					data.Close()
					data = nil
				}
				reply("550 no such directory")
				continue
			}
			reply("150 here it comes")
			if dc := accept(); dc != nil {
				io.WriteString(dc, s.listing(arg, cmd == "MLSD"))
				dc.Close()
			}
			reply("226 done")
		case "RETR":
			d, ok := s.Get(abs(arg))
			if !ok {
				if data != nil {
					data.Close()
					data = nil
				}
				reply("550 file not found")
				continue
			}
			reply("150 sending")
			if dc := accept(); dc != nil {
				if rest > int64(len(d)) {
					rest = int64(len(d))
				}
				dc.Write(d[rest:])
				dc.Close()
			}
			rest = 0
			reply("226 done")
		case "STOR":
			reply("150 receiving")
			if dc := accept(); dc != nil {
				body, _ := io.ReadAll(dc)
				dc.Close()
				s.mu.Lock()
				old := s.files[abs(arg)]
				if rest > 0 && int(rest) <= len(old) {
					body = append(append([]byte(nil), old[:rest]...), body...)
				}
				s.files[abs(arg)] = body
				s.mu.Unlock()
			}
			rest = 0
			reply("226 done")
		case "DELE":
			s.mu.Lock()
			_, ok := s.files[abs(arg)]
			delete(s.files, abs(arg))
			s.mu.Unlock()
			if ok {
				reply("250 deleted")
			} else {
				reply("550 file not found")
			}
		case "MKD":
			s.mu.Lock()
			s.dirs[abs(arg)] = true
			s.mu.Unlock()
			reply(`257 "%s" created`, abs(arg))
		case "RMD":
			s.mu.Lock()
			delete(s.dirs, abs(arg))
			s.mu.Unlock()
			reply("250 removed")
		case "RNFR":
			rest = 0
			reply("350 ready")
			line, _ = r.ReadString('\n')
			_, to, _ := strings.Cut(strings.TrimRight(line, "\r\n"), " ")
			s.mu.Lock()
			if d, ok := s.files[abs(arg)]; ok {
				s.files[abs(to)] = d
				delete(s.files, abs(arg))
				reply("250 renamed")
			} else {
				reply("550 file not found")
			}
			s.mu.Unlock()
		case "SITE":
			reply("200 ok")
		case "MFMT":
			reply("500 not supported")
		default:
			reply("500 unknown command")
		}
	}
}

// MkDir creates a directory (absolute path).
func (s *Server) MkDir(p string) {
	s.mu.Lock()
	s.dirs[path.Clean("/"+p)] = true
	s.mu.Unlock()
}

// Files lists the stored file paths.
func (s *Server) Files() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.files))
	for n := range s.files {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
