package connection

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/didou07/dcc-lite/src/config"
)

func listen(t *testing.T) (net.Listener, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			_ = c.Close()
		}
	}()
	t.Cleanup(func() { _ = l.Close() })
	return l, l.Addr().(*net.TCPAddr).Port
}

func closedPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return port
}

func TestTCPCheckerReportsOnlinePartialAndOffline(t *testing.T) {
	_, ftp := listen(t)
	_, telnet := listen(t)
	profile := config.Profile{ID: "test_profile_1234", Name: "Box", Host: "127.0.0.1", Enabled: true, FTPPort: ftp, TelnetPort: telnet}
	c := &TCPChecker{Timeout: time.Second}

	if s := c.Check(context.Background(), profile); s.State != "online" || len(s.Services) != 2 {
		t.Fatalf("expected online, got %#v", s)
	}
	profile.TelnetPort = closedPort(t)
	if s := c.Check(context.Background(), profile); s.State != "partial" {
		t.Fatalf("expected partial, got %#v", s)
	}
	profile.FTPPort = closedPort(t)
	if s := c.Check(context.Background(), profile); s.State != "offline" || s.Message == "" {
		t.Fatalf("expected offline, got %#v", s)
	}
}

func TestManagerKeepsLatestStatusAndForgets(t *testing.T) {
	_, ftp := listen(t)
	m := NewManager(&TCPChecker{Timeout: time.Second})
	profile := config.Profile{ID: "test_profile_1234", Name: "Box", Host: "127.0.0.1", Enabled: true, FTPPort: ftp, TelnetPort: closedPort(t)}
	if s := m.Test(context.Background(), profile); s.State != "partial" {
		t.Fatalf("unexpected status %#v", s)
	}
	if got, ok := m.Get(profile.ID); !ok || got.State != "partial" {
		t.Fatalf("status not stored: %#v %v", got, ok)
	}
	m.Forget(profile.ID)
	if _, ok := m.Get(profile.ID); ok {
		t.Fatal("status should be forgotten")
	}
}
