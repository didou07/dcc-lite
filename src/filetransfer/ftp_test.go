package filetransfer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

// miniFTP is a tiny in-memory FTP server, just enough to exercise the client.
type miniFTP struct {
	t        *testing.T
	ln       net.Listener
	mu       sync.Mutex
	files    map[string][]byte
	dirs     map[string]bool
	noMLSD   bool // answer MLSD with 500 so the client falls back to LIST
	noEPSV   bool // answer EPSV with 502 so the client falls back to PASV
	password string
	conns    []net.Conn
}

func startMiniFTP(t *testing.T) *miniFTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &miniFTP{t: t, ln: ln, files: map[string][]byte{}, dirs: map[string]bool{"/": true}, password: "secret"}
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
		s.dropAll()
	})
	return s
}

func (s *miniFTP) port() int { return s.ln.Addr().(*net.TCPAddr).Port }

func (s *miniFTP) dropAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.conns {
		c.Close()
	}
	s.conns = nil
}

func (s *miniFTP) put(name string, data []byte) {
	s.mu.Lock()
	s.files[name] = append([]byte(nil), data...)
	s.mu.Unlock()
}

func (s *miniFTP) get(name string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, ok := s.files[name]
	return append([]byte(nil), d...), ok
}

func (s *miniFTP) listing(dir string, mlsd bool) string {
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

func (s *miniFTP) serve(c net.Conn) {
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
			if user != "" && arg == s.password {
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
			if s.noEPSV {
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
			if d, ok := s.get(abs(arg)); ok {
				reply("213 %d", len(d))
			} else {
				reply("550 file not found")
			}
		case "MLSD", "LIST":
			if cmd == "MLSD" && s.noMLSD {
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
			d, ok := s.get(abs(arg))
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

func (s *miniFTP) opts() FTPOptions {
	return FTPOptions{Host: "127.0.0.1", Port: s.port(), Username: "root", Password: "secret"}
}

func openTestClient(t *testing.T, s *miniFTP) *FTPClient {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c, err := OpenFTP(ctx, s.opts())
	if err != nil {
		t.Fatalf("OpenFTP: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(42)).Read(b)
	return b
}

func TestFTPLoginAndBadPassword(t *testing.T) {
	s := startMiniFTP(t)
	openTestClient(t, s)
	bad := s.opts()
	bad.Password = "wrong"
	if _, err := OpenFTP(testCtx(t), bad); err == nil {
		t.Fatal("wrong password accepted")
	}
	bad = s.opts()
	bad.Password = "a\r\nQUIT"
	if _, err := OpenFTP(testCtx(t), bad); err == nil {
		t.Fatal("control characters in the password were not rejected")
	}
	if _, err := OpenFTP(testCtx(t), FTPOptions{Host: "", Port: 21}); err == nil {
		t.Fatal("empty host accepted")
	}
}

func TestFTPListMLSDAndFallbackToLIST(t *testing.T) {
	for _, noMLSD := range []bool{false, true} {
		t.Run(fmt.Sprintf("noMLSD=%v", noMLSD), func(t *testing.T) {
			s := startMiniFTP(t)
			s.noMLSD = noMLSD
			s.dirs["/etc"] = true
			s.dirs["/etc/enigma2"] = true
			s.put("/etc/hosts", []byte("12345"))
			c := openTestClient(t, s)
			entries, err := c.List(testCtx(t), "/etc")
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]core.FileEntry{}
			for _, e := range entries {
				got[e.Name] = e
			}
			if len(got) != 2 || !got["enigma2"].IsDir || got["hosts"].IsDir || got["hosts"].Size != 5 || got["hosts"].Path != "/etc/hosts" {
				t.Fatalf("entries = %+v", entries)
			}
		})
	}
}

func TestFTPListMissingFolderReportsNotFound(t *testing.T) {
	s := startMiniFTP(t)
	c := openTestClient(t, s)
	if _, err := c.List(testCtx(t), "/nope"); !errors.Is(err, ErrPathNotFound) {
		t.Fatalf("err = %v, want ErrPathNotFound", err)
	}
}

func TestFTPUploadDownloadRoundTrip(t *testing.T) {
	for _, noEPSV := range []bool{false, true} {
		t.Run(fmt.Sprintf("noEPSV=%v", noEPSV), func(t *testing.T) {
			s := startMiniFTP(t)
			s.noEPSV = noEPSV
			c := openTestClient(t, s)
			dir := t.TempDir()
			src := filepath.Join(dir, "src.bin")
			payload := randomBytes(300 << 10)
			if err := os.WriteFile(src, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			var last core.TransferProgress
			if err := c.Upload(testCtx(t), src, "/up.bin", func(p core.TransferProgress) { last = p }); err != nil {
				t.Fatal(err)
			}
			if last.BytesDone != int64(len(payload)) {
				t.Errorf("final progress = %+v", last)
			}
			if got, _ := s.get("/up.bin"); !bytes.Equal(got, payload) {
				t.Fatal("uploaded content differs")
			}
			dst := filepath.Join(dir, "sub", "dst.bin")
			if err := c.Download(testCtx(t), "/up.bin", dst, nil); err != nil {
				t.Fatal(err)
			}
			if got, _ := os.ReadFile(dst); !bytes.Equal(got, payload) {
				t.Fatal("downloaded content differs")
			}
			if matches, _ := filepath.Glob(filepath.Join(dir, "sub", ".dcclite-download-*")); len(matches) != 0 {
				t.Errorf("temporary files left behind: %v", matches)
			}
		})
	}
}

func TestFTPDownloadNeverOverwritesAndCleansUpOnFailure(t *testing.T) {
	s := startMiniFTP(t)
	c := openTestClient(t, s)
	dir := t.TempDir()
	existing := filepath.Join(dir, "keep.txt")
	os.WriteFile(existing, []byte("mine"), 0o600)
	s.put("/keep.txt", []byte("remote"))
	if err := c.Download(testCtx(t), "/keep.txt", existing, nil); err == nil {
		t.Fatal("existing local file was overwritten")
	}
	if b, _ := os.ReadFile(existing); string(b) != "mine" {
		t.Fatal("existing file modified")
	}
	missing := filepath.Join(dir, "missing.txt")
	if err := c.Download(testCtx(t), "/missing.txt", missing, nil); err == nil {
		t.Fatal("missing remote file reported success")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("a file was created for a failed download")
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".dcclite-download-*"))
	if len(left) != 0 {
		t.Fatalf("temp files left behind: %v", left)
	}
}

func TestFTPResumeDownload(t *testing.T) {
	s := startMiniFTP(t)
	c := openTestClient(t, s)
	payload := randomBytes(200 << 10)
	s.put("/big.bin", payload)
	dir := t.TempDir()

	partial := filepath.Join(dir, "p.part")
	os.WriteFile(partial, payload[:70000], 0o600)
	if err := c.ResumeDownload(testCtx(t), "/big.bin", partial, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(partial); !bytes.Equal(got, payload) {
		t.Fatal("resumed file differs from the original")
	}
	// already complete: succeeds without changing anything
	if err := c.ResumeDownload(testCtx(t), "/big.bin", partial, nil); err != nil {
		t.Fatal(err)
	}
	// no partial yet: starts from scratch
	fresh := filepath.Join(dir, "fresh.part")
	if err := c.ResumeDownload(testCtx(t), "/big.bin", fresh, nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(fresh); !bytes.Equal(got, payload) {
		t.Fatal("fresh resume differs")
	}
	// local partial bigger than remote file must be refused, not truncated
	huge := filepath.Join(dir, "huge.part")
	os.WriteFile(huge, append(payload, 1, 2, 3), 0o600)
	if err := c.ResumeDownload(testCtx(t), "/big.bin", huge, nil); err == nil {
		t.Fatal("oversized partial accepted")
	}
	// symlinks are refused
	link := filepath.Join(dir, "link.part")
	if err := os.Symlink(fresh, link); err == nil {
		if err := c.ResumeDownload(testCtx(t), "/big.bin", link, nil); err == nil {
			t.Fatal("symlinked partial accepted")
		}
	}
}

func TestFTPResumeUpload(t *testing.T) {
	s := startMiniFTP(t)
	c := openTestClient(t, s)
	payload := randomBytes(150 << 10)
	src := filepath.Join(t.TempDir(), "src.bin")
	os.WriteFile(src, payload, 0o600)

	s.put("/r.bin", payload[:50000])
	if err := c.ResumeUpload(testCtx(t), src, "/r.bin", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.get("/r.bin"); !bytes.Equal(got, payload) {
		t.Fatal("resumed upload differs")
	}
	if err := c.ResumeUpload(testCtx(t), src, "/r.bin", nil); err != nil {
		t.Fatalf("already complete upload failed: %v", err)
	}
	if err := c.ResumeUpload(testCtx(t), src, "/new.bin", nil); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.get("/new.bin"); !bytes.Equal(got, payload) {
		t.Fatal("upload from scratch differs")
	}
	s.put("/toobig.bin", append(payload, 9))
	if err := c.ResumeUpload(testCtx(t), src, "/toobig.bin", nil); err == nil {
		t.Fatal("remote file larger than the source must not be silently continued")
	}
}

func TestFTPFileOperations(t *testing.T) {
	s := startMiniFTP(t)
	c := openTestClient(t, s)
	ctx := testCtx(t)
	if err := c.Mkdir(ctx, "/new"); err != nil {
		t.Fatal(err)
	}
	s.put("/new/a.txt", []byte("hello"))
	if n, err := c.Size(ctx, "/new/a.txt"); err != nil || n != 5 {
		t.Fatalf("size = %d, %v", n, err)
	}
	if err := c.Rename(ctx, "/new/a.txt", "/new/b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s.get("/new/b.txt"); !ok {
		t.Fatal("rename did not happen")
	}
	if err := c.Rename(ctx, "/new/zzz", "/new/y"); err == nil {
		t.Fatal("renaming a missing file succeeded")
	}
	if err := c.Chmod(ctx, "/new/b.txt", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := c.Chmod(ctx, "/new/b.txt", 0o10000); err == nil {
		t.Fatal("invalid mode accepted")
	}
	if err := c.SetModTime(ctx, "/new/b.txt", time.Now()); !errors.Is(err, core.ErrMetadataUnsupported) {
		t.Fatalf("SetModTime err = %v, want ErrMetadataUnsupported", err)
	}
	if err := c.Remove(ctx, "/new/b.txt", false); err != nil {
		t.Fatal(err)
	}
	if err := c.Remove(ctx, "/new/b.txt", false); err == nil {
		t.Fatal("removing a missing file succeeded")
	}
	if err := c.Remove(ctx, "/new", true); err != nil {
		t.Fatal(err)
	}
}

func TestFTPRejectsControlCharactersInPaths(t *testing.T) {
	s := startMiniFTP(t)
	c := openTestClient(t, s)
	ctx := testCtx(t)
	bad := "/a\r\nDELE /important"
	if _, err := c.List(ctx, bad); err == nil {
		t.Error("List accepted a path with CRLF")
	}
	if err := c.Remove(ctx, bad, false); err == nil {
		t.Error("Remove accepted a path with CRLF")
	}
	if err := c.Mkdir(ctx, ""); err == nil {
		t.Error("Mkdir accepted an empty path")
	}
}

func TestFTPContextCancelStopsTransfer(t *testing.T) {
	s := startMiniFTP(t)
	c := openTestClient(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.List(ctx, "/"); err == nil {
		t.Fatal("cancelled context did not stop List")
	}
}

func TestResilientFTPReconnectsAfterServerDropsConnection(t *testing.T) {
	s := startMiniFTP(t)
	s.put("/f.txt", []byte("abc"))
	r, err := OpenResilientFTP(testCtx(t), s.opts())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	if _, err := r.List(testCtx(t), "/"); err != nil {
		t.Fatal(err)
	}
	s.dropAll() // simulates the receiver rebooting or the network blipping
	time.Sleep(50 * time.Millisecond)
	entries, err := r.List(testCtx(t), "/")
	if err != nil {
		t.Fatalf("List after a dropped connection: %v", err)
	}
	if len(entries) != 1 || entries[0].Name != "f.txt" {
		t.Fatalf("entries = %+v", entries)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := r.List(testCtx(t), "/"); err == nil {
		t.Fatal("closed session still works")
	}
}

func TestParsePASVAndEPSV(t *testing.T) {
	if p, err := parsePASVPort("227 Entering Passive Mode (192,168,1,5,195,80)"); err != nil || p != 195*256+80 {
		t.Errorf("PASV = %d, %v", p, err)
	}
	for _, bad := range []string{"227 nothing", "(1,2,3,4,5)", "(1,2,3,4,256,1)", "(1,2,3,4,0,0)", "(a,b,c,d,e,f)"} {
		if _, err := parsePASVPort(bad); err == nil {
			t.Errorf("PASV %q accepted", bad)
		}
	}
	if p, err := parseEPSVPort("229 Entering Extended Passive Mode (|||50000|)"); err != nil || p != 50000 {
		t.Errorf("EPSV = %d, %v", p, err)
	}
	for _, bad := range []string{"229 x", "(|||0|)", "(|||70000|)", "(|||abc|)", "(||1|2|)"} {
		if _, err := parseEPSVPort(bad); err == nil {
			t.Errorf("EPSV %q accepted", bad)
		}
	}
}

func TestParseListings(t *testing.T) {
	mlsd := "type=cdir;modify=20240101000000; .\r\ntype=pdir; ..\r\n" +
		"type=dir;modify=20240102030405;unix.mode=0755; enigma2\r\n" +
		"type=file;size=1234;modify=20240102030405;unix.mode=0644; my file.txt\r\nbroken line without facts\r\n"
	e := parseMLSD([]byte(mlsd), "/etc")
	if len(e) != 2 || e[0].Name != "enigma2" || !e[0].IsDir || e[1].Name != "my file.txt" || e[1].Size != 1234 || e[1].Path != "/etc/my file.txt" {
		t.Fatalf("mlsd = %+v", e)
	}
	if e[1].ModifiedAt.Year() != 2024 || e[1].Mode != "0644" {
		t.Errorf("mlsd facts lost: %+v", e[1])
	}

	list := "total 8\r\n" +
		"drwxr-xr-x 2 root root 4096 Jan  2  2024 bouquets\r\n" +
		"-rw-r--r-- 1 root root 99 Mar 15 10:30 name with spaces.tv\r\n" +
		"lrwxrwxrwx 1 root root 7 Jan  2  2024 link -> /target\r\n" +
		"drwxr-xr-x 2 root root 4096 Jan  2  2024 .\r\n"
	l := parseFTPList([]byte(list), "/etc")
	if len(l) != 3 {
		t.Fatalf("list = %+v", l)
	}
	if !l[0].IsDir || l[1].Name != "name with spaces.tv" || l[1].Size != 99 || l[2].Name != "link" {
		t.Fatalf("list = %+v", l)
	}
}

func TestParsePWDReplyAndCleanJoin(t *testing.T) {
	if got := parsePWDReply(`257 "/home/ro""ot" is current`); got != `/home/ro"ot` {
		t.Errorf("pwd = %q", got)
	}
	if parsePWDReply("257 no quotes") != "" {
		t.Error("expected empty result")
	}
	if cleanRemoteJoin("/etc", "../x") != "/x" || cleanRemoteJoin("/etc", "/abs/./p") != "/abs/p" || cleanRemoteJoin("/etc", "a") != "/etc/a" {
		t.Error("cleanRemoteJoin wrong")
	}
}

func TestListingKeepsSymlinkTarget(t *testing.T) {
	list := parseFTPList([]byte("lrwxrwxrwx 1 root root 28 Nov 20 10:00 oscam.server -> /etc/tuxbox/config/oscam.server\r\n"), "/media/x")
	if len(list) != 1 || list[0].Name != "oscam.server" || list[0].LinkTarget != "/etc/tuxbox/config/oscam.server" {
		t.Fatalf("LIST: %+v", list)
	}
	mlsd := parseMLSD([]byte("type=OS.unix=slink:/etc/a;size=5; a.cfg\r\n"), "/d")
	if len(mlsd) != 1 || mlsd[0].LinkTarget != "/etc/a" {
		t.Fatalf("MLSD: %+v", mlsd)
	}
}
