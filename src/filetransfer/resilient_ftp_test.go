package filetransfer

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// startMiniFTP answers login, TYPE, NOOP and MKD. dropFirst makes it close the
// first control connection right after login, like a receiver that timed out.
func startMiniFTP(t *testing.T) (port int, accepted *atomic.Int32, dropFirst func()) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	accepted = &atomic.Int32{}
	drop := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			n := accepted.Add(1)
			go func(conn net.Conn, first bool) {
				defer conn.Close()
				_, _ = io.WriteString(conn, "220 ready\r\n")
				reader := bufio.NewReader(conn)
				for {
					if first {
						_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
					}
					line, err := reader.ReadString('\n')
					if err != nil {
						select {
						case <-drop:
							return
						default:
						}
						if ne, ok := err.(net.Error); ok && ne.Timeout() {
							continue
						}
						return
					}
					command := strings.ToUpper(strings.Fields(line)[0])
					switch command {
					case "USER":
						_, _ = io.WriteString(conn, "331 password\r\n")
					case "PASS":
						_, _ = io.WriteString(conn, "230 ok\r\n")
					case "TYPE":
						_, _ = io.WriteString(conn, "200 binary\r\n")
					case "NOOP":
						_, _ = io.WriteString(conn, "200 ok\r\n")
					case "MKD":
						_, _ = io.WriteString(conn, "257 created\r\n")
					case "QUIT":
						_, _ = io.WriteString(conn, "221 bye\r\n")
						return
					default:
						_, _ = io.WriteString(conn, "502 no\r\n")
					}
				}
			}(conn, n == 1)
		}
	}()
	return listener.Addr().(*net.TCPAddr).Port, accepted, func() { drop <- struct{}{} }
}

func TestResilientFTPReconnectsAfterTheReceiverDropsTheConnection(t *testing.T) {
	port, accepted, dropFirst := startMiniFTP(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := OpenResilientFTP(ctx, FTPOptions{Host: "127.0.0.1", Port: port, Username: "root"})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if err := session.Mkdir(ctx, "/one"); err != nil {
		t.Fatal(err)
	}

	dropFirst() // the receiver closes the idle control connection
	time.Sleep(200 * time.Millisecond)
	session.mu.Lock()
	session.lastUsed = time.Now().Add(-time.Minute) // the session has been idle
	session.mu.Unlock()

	if err := session.Mkdir(ctx, "/two"); err != nil {
		t.Fatalf("operation after a dropped connection failed: %v", err)
	}
	if got := accepted.Load(); got != 2 {
		t.Fatalf("control connections opened = %d, want 2 (one reconnect)", got)
	}
}

func TestResilientFTPKeepsWorkingOnTheSameConnection(t *testing.T) {
	port, accepted, _ := startMiniFTP(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session, err := OpenResilientFTP(ctx, FTPOptions{Host: "127.0.0.1", Port: port})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	for _, dir := range []string{"/a", "/b", "/c"} {
		if err := session.Mkdir(ctx, dir); err != nil {
			t.Fatal(err)
		}
	}
	if got := accepted.Load(); got != 1 {
		t.Fatalf("control connections opened = %d, want 1", got)
	}
	if err := session.Close(); err != nil {
		t.Fatal(err)
	}
	if err := session.Mkdir(ctx, "/d"); err == nil {
		t.Fatal("closed session accepted an operation")
	}
}
