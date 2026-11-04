package filetransfer

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/didou07/dcc-lite/src/core"
)

func TestDefaultFTPUsernameUsesEnigma2Root(t *testing.T) {
	if got := defaultFTPUsername(""); got != "root" {
		t.Fatalf("empty FTP username = %q, want root", got)
	}
	if got := defaultFTPUsername("  "); got != "root" {
		t.Fatalf("blank FTP username = %q, want root", got)
	}
	if got := defaultFTPUsername("anonymous"); got != "anonymous" {
		t.Fatalf("explicit FTP username = %q, want anonymous", got)
	}
}

func TestFTPCommandPreservesEmptyPASSArgument(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()
	client := &FTPClient{control: clientConn, reader: bufio.NewReader(clientConn)}
	gotLine := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(serverConn).ReadString('\n')
		gotLine <- line
		_, _ = io.WriteString(serverConn, "230 logged in\r\n")
	}()
	code, _, err := client.commandWithArgument("PASS", "")
	if err != nil || code != 230 {
		t.Fatalf("PASS response = %d, %v", code, err)
	}
	if line := <-gotLine; line != "PASS \r\n" {
		t.Fatalf("empty PASS command = %q, want %q", line, "PASS \r\n")
	}
}

func TestFTPSetModTimeUsesUTCAndDetectsUnsupportedExtension(t *testing.T) {
	t.Run("supported", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()
		client := &FTPClient{control: clientConn, reader: bufio.NewReader(clientConn)}
		serverErr := make(chan error, 1)
		go func() {
			line, err := bufio.NewReader(serverConn).ReadString('\n')
			if err != nil {
				serverErr <- err
				return
			}
			if line != "MFMT 20260304140506 /tmp/config file\r\n" {
				serverErr <- fmt.Errorf("unexpected MFMT command: %q", line)
				return
			}
			_, err = io.WriteString(serverConn, "213 modify time set\r\n")
			serverErr <- err
		}()
		zone := time.FixedZone("UTC+2", 2*60*60)
		modified := time.Date(2026, time.March, 4, 16, 5, 6, 0, zone)
		if err := client.SetModTime(context.Background(), "/tmp/config file", modified); err != nil {
			t.Fatal(err)
		}
		if err := <-serverErr; err != nil {
			t.Fatal(err)
		}
	})

	t.Run("unsupported", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		defer serverConn.Close()
		client := &FTPClient{control: clientConn, reader: bufio.NewReader(clientConn)}
		go func() {
			_, _ = bufio.NewReader(serverConn).ReadString('\n')
			_, _ = io.WriteString(serverConn, "502 MFMT not implemented\r\n")
		}()
		if err := client.SetModTime(context.Background(), "/tmp/config", time.Now()); !errors.Is(err, core.ErrMetadataUnsupported) {
			t.Fatalf("unsupported MFMT error = %v", err)
		}
	})
}

func TestFTPFallsBackToPASVWhenEPSVDataConnectFails(t *testing.T) {
	controlListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer controlListener.Close()
	deadListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	_, deadPortText, _ := net.SplitHostPort(deadListener.Addr().String())
	_ = deadListener.Close()
	dataListenerCh := make(chan net.Listener, 1)
	serverErr := make(chan error, 1)
	go func() {
		conn, acceptErr := controlListener.Accept()
		if acceptErr != nil {
			serverErr <- acceptErr
			return
		}
		defer conn.Close()
		reader := bufio.NewReader(conn)
		line, readErr := reader.ReadString('\n')
		if readErr != nil || !strings.HasPrefix(line, "EPSV") {
			serverErr <- io.ErrUnexpectedEOF
			return
		}
		_, _ = io.WriteString(conn, "229 Entering Extended Passive Mode (|||"+deadPortText+"|)\r\n")
		line, readErr = reader.ReadString('\n')
		if readErr != nil || !strings.HasPrefix(line, "PASV") {
			serverErr <- io.ErrUnexpectedEOF
			return
		}
		dataListener, listenErr := net.Listen("tcp", "127.0.0.1:0")
		if listenErr != nil {
			serverErr <- listenErr
			return
		}
		dataListenerCh <- dataListener
		_, portText, _ := net.SplitHostPort(dataListener.Addr().String())
		port, _ := strconv.Atoi(portText)
		p1, p2 := port/256, port%256
		_, _ = fmt.Fprintf(conn, "227 Entering Passive Mode (127,0,0,1,%d,%d)\r\n", p1, p2)
	}()
	controlConn, err := net.Dial("tcp", controlListener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer controlConn.Close()
	client := &FTPClient{control: controlConn, reader: bufio.NewReader(controlConn), host: "127.0.0.1"}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	dataConn, err := client.openPassive(ctx)
	if err != nil {
		t.Fatalf("PASV fallback failed after EPSV data dial error: %v", err)
	}
	_ = dataConn.Close()
	dataListener := <-dataListenerCh
	defer dataListener.Close()
	_ = dataListener.(*net.TCPListener).SetDeadline(time.Now().Add(time.Second))
	accepted, err := dataListener.Accept()
	if err != nil {
		t.Fatalf("PASV data listener did not receive connection: %v", err)
	}
	_ = accepted.Close()
	select {
	case err := <-serverErr:
		if err != nil {
			t.Fatal(err)
		}
	default:
	}
}

func TestParsePassiveResponses(t *testing.T) {
	port, err := parseEPSVPort("229 Entering Extended Passive Mode (|||32123|)")
	if err != nil || port != 32123 {
		t.Fatalf("EPSV port = %d, %v", port, err)
	}
	port, err = parsePASVPort("227 Entering Passive Mode (10,1,2,3,125,7)")
	if err != nil || port != 32007 {
		t.Fatalf("PASV port = %d, %v", port, err)
	}
	if _, err := parsePASVPort("227 Entering Passive Mode (10,1,2,3,999,7)"); err == nil {
		t.Fatal("accepted invalid PASV response")
	}
}

func TestFTPReadLineRejectsOversizedReplyWithoutUnboundedRead(t *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	client := &FTPClient{control: clientConn, reader: bufio.NewReaderSize(clientConn, 1024)}
	writeDone := make(chan struct{})
	go func() {
		defer close(writeDone)
		_, _ = io.WriteString(serverConn, strings.Repeat("A", maxFTPReplyBytes+4096)+"\r\n")
	}()
	_, err := client.readLine()
	if err == nil || !strings.Contains(err.Error(), "size limit") {
		t.Fatalf("oversized FTP reply line error = %v", err)
	}
	if !client.closed {
		t.Fatal("FTP control connection remained reusable after a framing violation")
	}
	if _, _, commandErr := client.command("NOOP", ""); !errors.Is(commandErr, net.ErrClosed) {
		t.Fatalf("command after framing violation = %v, want closed connection", commandErr)
	}
	_ = serverConn.Close()
	<-writeDone
}

func TestFTPReadReplyRejectsOversizedAndUnterminatedMultilineReplies(t *testing.T) {
	t.Run("total size", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		client := &FTPClient{control: clientConn, reader: bufio.NewReader(clientConn)}
		writeDone := make(chan struct{})
		go func() {
			defer close(writeDone)
			_, _ = io.WriteString(serverConn, "220-start\r\n")
			for i := 0; i < 220; i++ {
				_, _ = io.WriteString(serverConn, strings.Repeat("x", 300)+"\r\n")
			}
			_, _ = io.WriteString(serverConn, "220 done\r\n")
		}()
		_, _, err := client.readReply()
		if err == nil || !strings.Contains(err.Error(), "multiline reply exceeds") {
			t.Fatalf("oversized multiline FTP reply error = %v", err)
		}
		_ = serverConn.Close()
		<-writeDone
	})

	t.Run("missing terminator", func(t *testing.T) {
		clientConn, serverConn := net.Pipe()
		defer clientConn.Close()
		client := &FTPClient{control: clientConn, reader: bufio.NewReader(clientConn)}
		writeDone := make(chan struct{})
		go func() {
			defer close(writeDone)
			for i := 0; i <= maxFTPReplyLines; i++ {
				_, _ = io.WriteString(serverConn, "220-continued\r\n")
			}
		}()
		_, _, err := client.readReply()
		if err == nil || !strings.Contains(err.Error(), "no terminating") {
			t.Fatalf("unterminated multiline FTP reply error = %v", err)
		}
		_ = serverConn.Close()
		<-writeDone
	})
}

func TestReadBoundedRejectsTruncatedFTPDirectoryListing(t *testing.T) {
	data, err := readBounded(strings.NewReader("123456789"), 8)
	if err == nil || data != nil {
		t.Fatalf("oversized listing data = %q, error = %v", data, err)
	}
	data, err = readBounded(strings.NewReader("12345678"), 8)
	if err != nil || string(data) != "12345678" {
		t.Fatalf("listing at exact limit = %q, error = %v", data, err)
	}
}

func TestParseMLSDAndLIST(t *testing.T) {
	data := []byte("type=dir;modify=20260101123456;unix.mode=0755; media folder\r\n" +
		"type=file;size=42;modify=20260102101010;unix.mode=0644; channels.conf\r\n" +
		"type=cdir; .\r\n")
	entries := parseMLSD(data, "/etc/enigma2")
	if len(entries) != 2 || !entries[0].IsDir || entries[1].Size != 42 || entries[1].Name != "channels.conf" {
		t.Fatalf("unexpected MLSD entries: %#v", entries)
	}
	list := []byte("drwxr-xr-x 2 root root 4096 Jan 01 12:34 media folder\r\n-rw-r--r-- 1 root root 42 Jan 02 2026 channels.conf\r\n")
	entries = parseFTPList(list, "/etc/enigma2")
	if len(entries) != 2 || entries[0].Name != "media folder" || !entries[0].IsDir || entries[1].Size != 42 {
		t.Fatalf("unexpected LIST entries: %#v", entries)
	}
}

func TestFTPClientListUploadDownloadAndMutations(t *testing.T) {
	address, uploaded, shutdown := startFTPTestServer(t)
	defer shutdown()
	host, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := OpenFTP(ctx, FTPOptions{Host: host, Port: port, Username: "", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	entries, err := client.List(ctx, "/")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 || entries[0].Name != "etc" || entries[1].Name != "settings file" {
		t.Fatalf("unexpected remote listing: %#v", entries)
	}

	root := t.TempDir()
	source := filepath.Join(root, "upload file.bin")
	if err := os.WriteFile(source, []byte("upload-content"), 0o600); err != nil {
		t.Fatal(err)
	}
	var lastDone int64
	if err := client.Upload(ctx, source, "/tmp/upload file.bin", func(p core.TransferProgress) { lastDone = p.BytesDone }); err != nil {
		t.Fatal(err)
	}
	if got := <-uploaded; string(got) != "upload-content" {
		t.Fatalf("uploaded data = %q", got)
	}
	if lastDone != int64(len("upload-content")) {
		t.Fatalf("upload progress = %d", lastDone)
	}

	destination := filepath.Join(root, "download.bin")
	if err := client.Download(ctx, "/tmp/download.bin", destination, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil || string(data) != "download-content" {
		t.Fatalf("download data %q, %v", data, err)
	}

	if err := client.Mkdir(ctx, "/tmp/new folder"); err != nil {
		t.Fatal(err)
	}
	if err := client.Rename(ctx, "/tmp/old", "/tmp/new"); err != nil {
		t.Fatal(err)
	}
	if err := client.Remove(ctx, "/tmp/file", false); err != nil {
		t.Fatal(err)
	}
	if err := client.Chmod(ctx, "/tmp/file", 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFTPListFallsBackToLISTWhenMLSDIsUnsupported(t *testing.T) {
	address, _, shutdown := startFTPTestServerWithMLSD(t, false)
	defer shutdown()
	host, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := OpenFTP(ctx, FTPOptions{Host: host, Port: port, Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	entries, err := client.List(ctx, "/")
	if err != nil {
		t.Fatalf("LIST fallback failed when MLSD is unsupported: %v", err)
	}
	if len(entries) != 2 || entries[0].Name != "etc" || entries[1].Name != "settings file" {
		t.Fatalf("unexpected LIST fallback entries: %#v", entries)
	}
}

type coreProgress = struct {
	BytesDone  int64
	BytesTotal int64
	Fraction   float64
}

func startFTPTestServer(t *testing.T) (string, <-chan []byte, func()) {
	return startFTPTestServerWithMLSD(t, true)
}

func startFTPTestServerWithMLSD(t *testing.T, supportsMLSD bool) (string, <-chan []byte, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	uploaded := make(chan []byte, 1)
	go func() {
		control, err := listener.Accept()
		if err != nil {
			return
		}
		defer control.Close()
		reader := bufioNewReader(control)
		_, _ = io.WriteString(control, "220 test FTP ready\r\n")
		var dataListener net.Listener
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			command, argument, _ := strings.Cut(line, " ")
			switch strings.ToUpper(command) {
			case "USER":
				if argument != "root" {
					_, _ = io.WriteString(control, "530 unexpected username\r\n")
				} else {
					_, _ = io.WriteString(control, "331 password required\r\n")
				}
			case "PASS":
				if argument != "secret" {
					_, _ = io.WriteString(control, "530 login incorrect\r\n")
				} else {
					_, _ = io.WriteString(control, "230 logged in\r\n")
				}
			case "TYPE":
				_, _ = io.WriteString(control, "200 binary mode\r\n")
			case "EPSV":
				if dataListener != nil {
					_ = dataListener.Close()
				}
				dataListener, err = net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					return
				}
				_, portText, _ := net.SplitHostPort(dataListener.Addr().String())
				_, _ = io.WriteString(control, "229 Entering Extended Passive Mode (|||"+portText+"|)\r\n")
			case "MLSD":
				if !supportsMLSD {
					_, _ = io.WriteString(control, "502 MLSD not implemented\r\n")
					continue
				}
				_, _ = io.WriteString(control, "150 opening data\r\n")
				dataConn, e := dataListener.Accept()
				if e != nil {
					return
				}
				_, _ = io.WriteString(dataConn, "type=dir; etc\r\ntype=file;size=12; settings file\r\n")
				_ = dataConn.Close()
				_ = dataListener.Close()
				dataListener = nil
				_, _ = io.WriteString(control, "226 listing complete\r\n")
			case "LIST":
				_, _ = io.WriteString(control, "150 opening data\r\n")
				dataConn, e := dataListener.Accept()
				if e != nil {
					return
				}
				_, _ = io.WriteString(dataConn, "drwxr-xr-x 2 root root 4096 Jan 01 12:34 etc\r\n-rw-r--r-- 1 root root 12 Jan 02 2026 settings file\r\n")
				_ = dataConn.Close()
				_ = dataListener.Close()
				dataListener = nil
				_, _ = io.WriteString(control, "226 listing complete\r\n")
			case "STOR":
				_, _ = io.WriteString(control, "150 opening data\r\n")
				dataConn, e := dataListener.Accept()
				if e != nil {
					return
				}
				payload, _ := io.ReadAll(dataConn)
				_ = dataConn.Close()
				_ = dataListener.Close()
				dataListener = nil
				uploaded <- payload
				_, _ = io.WriteString(control, "226 upload complete\r\n")
			case "RETR":
				_, _ = io.WriteString(control, "150 opening data\r\n")
				dataConn, e := dataListener.Accept()
				if e != nil {
					return
				}
				_, _ = io.WriteString(dataConn, "download-content")
				_ = dataConn.Close()
				_ = dataListener.Close()
				dataListener = nil
				_, _ = io.WriteString(control, "226 download complete\r\n")
			case "MKD":
				_, _ = io.WriteString(control, "257 directory created\r\n")
			case "RNFR":
				_, _ = io.WriteString(control, "350 ready for destination\r\n")
			case "RNTO":
				_, _ = io.WriteString(control, "250 renamed\r\n")
			case "DELE", "RMD", "SITE":
				_, _ = io.WriteString(control, "250 command complete\r\n")
			case "QUIT":
				_, _ = io.WriteString(control, "221 bye\r\n")
				return
			default:
				_, _ = io.WriteString(control, "502 unsupported\r\n")
			}
		}
	}()
	return listener.Addr().String(), uploaded, func() { _ = listener.Close() }
}

// The helper keeps the fixture code small while preserving a buffered control channel.
func bufioNewReader(conn net.Conn) *bufio.Reader { return bufio.NewReader(conn) }

func startFTPResumeFixture(t *testing.T, initial map[string][]byte) (string, func() map[string][]byte, func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	files := make(map[string][]byte, len(initial))
	for name, data := range initial {
		files[name] = append([]byte(nil), data...)
	}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		control, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer control.Close()
		reader := bufio.NewReader(control)
		_, _ = io.WriteString(control, "220 resume fixture ready\r\n")
		var dataListener net.Listener
		var restart int64
		var restPending bool
		for {
			line, readErr := reader.ReadString('\n')
			if readErr != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			command, argument, _ := strings.Cut(line, " ")
			switch strings.ToUpper(command) {
			case "USER":
				_, _ = io.WriteString(control, "331 password required\r\n")
			case "PASS":
				_, _ = io.WriteString(control, "230 logged in\r\n")
			case "TYPE":
				_, _ = io.WriteString(control, "200 binary mode\r\n")
			case "SIZE":
				mu.Lock()
				data, exists := files[argument]
				mu.Unlock()
				if !exists {
					_, _ = io.WriteString(control, "550 file not found\r\n")
				} else {
					_, _ = fmt.Fprintf(control, "213 %d\r\n", len(data))
				}
			case "EPSV":
				if dataListener != nil {
					_ = dataListener.Close()
				}
				dataListener, acceptErr = net.Listen("tcp", "127.0.0.1:0")
				if acceptErr != nil {
					return
				}
				_, portText, _ := net.SplitHostPort(dataListener.Addr().String())
				_, _ = io.WriteString(control, "229 Entering Extended Passive Mode (|||"+portText+"|)\r\n")
			case "REST":
				restart, _ = strconv.ParseInt(argument, 10, 64)
				restPending = true
				_, _ = io.WriteString(control, "350 restart accepted\r\n")
			case "STOR":
				if !restPending {
					_, _ = io.WriteString(control, "503 REST must immediately precede STOR\r\n")
					continue
				}
				restPending = false
				_, _ = io.WriteString(control, "150 opening data\r\n")
				dataConn, dataErr := dataListener.Accept()
				if dataErr != nil {
					return
				}
				payload, _ := io.ReadAll(dataConn)
				_ = dataConn.Close()
				_ = dataListener.Close()
				dataListener = nil
				mu.Lock()
				prefix := []byte(nil)
				if restart > 0 {
					previous := files[argument]
					if restart > int64(len(previous)) {
						mu.Unlock()
						_, _ = io.WriteString(control, "551 restart beyond file size\r\n")
						continue
					}
					prefix = append(prefix, previous[:restart]...)
				}
				files[argument] = append(prefix, payload...)
				mu.Unlock()
				restart = 0
				_, _ = io.WriteString(control, "226 upload complete\r\n")
			case "RETR":
				if !restPending {
					_, _ = io.WriteString(control, "503 REST must immediately precede RETR\r\n")
					continue
				}
				restPending = false
				mu.Lock()
				data := append([]byte(nil), files[argument]...)
				mu.Unlock()
				if restart > int64(len(data)) {
					_, _ = io.WriteString(control, "551 restart beyond file size\r\n")
					continue
				}
				_, _ = io.WriteString(control, "150 opening data\r\n")
				dataConn, dataErr := dataListener.Accept()
				if dataErr != nil {
					return
				}
				_, _ = dataConn.Write(data[restart:])
				_ = dataConn.Close()
				_ = dataListener.Close()
				dataListener = nil
				restart = 0
				_, _ = io.WriteString(control, "226 download complete\r\n")
			case "QUIT":
				_, _ = io.WriteString(control, "221 bye\r\n")
				return
			default:
				_, _ = io.WriteString(control, "502 unsupported\r\n")
			}
		}
	}()
	address := listener.Addr().String()
	snapshot := func() map[string][]byte {
		mu.Lock()
		defer mu.Unlock()
		out := make(map[string][]byte, len(files))
		for name, data := range files {
			out[name] = append([]byte(nil), data...)
		}
		return out
	}
	shutdown := func() {
		_ = listener.Close()
		select {
		case <-finished:
		case <-time.After(time.Second):
		}
	}
	return address, snapshot, shutdown
}

func TestFTPResumesInterruptedUploadAndDownload(t *testing.T) {
	remoteUploadPartial := []byte("upload-prefix-")
	fullUpload := []byte("upload-prefix-and-the-rest")
	fullDownload := []byte("download-prefix-and-the-rest")
	address, snapshot, shutdown := startFTPResumeFixture(t, map[string][]byte{
		"/upload.bin":   remoteUploadPartial,
		"/download.bin": fullDownload,
	})
	defer shutdown()
	host, portText, _ := net.SplitHostPort(address)
	port, _ := strconv.Atoi(portText)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client, err := OpenFTP(ctx, FTPOptions{Host: host, Port: port, Username: "root", Password: "secret"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	root := t.TempDir()
	upload := filepath.Join(root, "upload.bin")
	if err := os.WriteFile(upload, fullUpload, 0o600); err != nil {
		t.Fatal(err)
	}
	var uploadProgress core.TransferProgress
	if err := client.ResumeUpload(ctx, upload, "/upload.bin", func(p core.TransferProgress) { uploadProgress = p }); err != nil {
		t.Fatalf("resume FTP upload: %v", err)
	}
	if got := string(snapshot()["/upload.bin"]); got != string(fullUpload) {
		t.Fatalf("resumed remote upload = %q, want %q", got, fullUpload)
	}
	if uploadProgress.BytesDone != int64(len(fullUpload)) || uploadProgress.Fraction != 1 {
		t.Fatalf("upload progress after resume = %#v", uploadProgress)
	}

	partial := filepath.Join(root, "download.part")
	prefix := []byte("download-prefix-")
	if err := os.WriteFile(partial, prefix, 0o600); err != nil {
		t.Fatal(err)
	}
	var downloadProgress core.TransferProgress
	if err := client.ResumeDownload(ctx, "/download.bin", partial, func(p core.TransferProgress) { downloadProgress = p }); err != nil {
		t.Fatalf("resume FTP download: %v", err)
	}
	got, err := os.ReadFile(partial)
	if err != nil || !bytes.Equal(got, fullDownload) {
		t.Fatalf("resumed local download = %q, %v", got, err)
	}
	if downloadProgress.BytesDone != int64(len(fullDownload)) || downloadProgress.Fraction != 1 {
		t.Fatalf("download progress after resume = %#v", downloadProgress)
	}
}

func TestFTPRejectsCommandInjection(t *testing.T) {
	if !invalidFTPArgument("/tmp/name\r\nDELE /important") {
		t.Fatal("FTP command injection was not rejected")
	}
	if err := validateRemotePath("/tmp/name\nNEXT"); err == nil {
		t.Fatal("remote path containing a newline was accepted")
	}
}

func TestDownloadDoesNotOverwriteExistingLocalFile(t *testing.T) {
	root := t.TempDir()
	dest := filepath.Join(root, "existing")
	if err := os.WriteFile(dest, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The destination guard is checked before opening a data channel.
	client := &FTPClient{host: "127.0.0.1"}
	err := client.Download(context.Background(), "/remote", dest, nil)
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("expected overwrite refusal, got %v", err)
	}
	data, _ := os.ReadFile(dest)
	if !bytes.Equal(data, []byte("keep")) {
		t.Fatal("existing destination was modified")
	}
}
