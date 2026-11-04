package netscan

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
)

func TestParseRangeForms(t *testing.T) {
	cases := []struct {
		in    string
		count int
		first string
		last  string
	}{
		{"192.168.1.0/24", 254, "192.168.1.1", "192.168.1.254"},
		{"192.168.1.10-50", 41, "192.168.1.10", "192.168.1.50"},
		{"192.168.1.10-192.168.1.12", 3, "192.168.1.10", "192.168.1.12"},
		{"10.0.0.5", 1, "10.0.0.5", "10.0.0.5"},
		{"192.168.1.7/32", 1, "192.168.1.7", "192.168.1.7"},
		{"10.1.0.0/22", 1022, "10.1.0.1", "10.1.3.254"},
		{" 172.16.5.1 - 3 ", 3, "172.16.5.1", "172.16.5.3"},
	}
	for _, c := range cases {
		got, err := ParseRange(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if len(got) != c.count || got[0].String() != c.first || got[len(got)-1].String() != c.last {
			t.Errorf("%q = %d addresses %v..%v, want %d %s..%s", c.in, len(got), got[0], got[len(got)-1], c.count, c.first, c.last)
		}
	}
}

func TestParseRangeRejectsUnsafeInput(t *testing.T) {
	for _, bad := range []string{
		"", "nonsense", "8.8.8.8", "8.8.8.0/24", "192.168.1.0/16", "10.0.0.0/8", "192.168.1.50-10",
		"192.168.1.1-300", "192.168.1.1-8.8.8.8", "::1", "2001:db8::/32", "172.15.0.1", "192.168.1.1-192.168.9.1",
		"192.168.255.250-193.0.0.5", "1.2.3.4-5",
	} {
		if _, err := ParseRange(bad); !errors.Is(err, ErrRange) {
			t.Errorf("%q accepted (err=%v)", bad, err)
		}
	}
}

func listen(t *testing.T, banner string) (int, func()) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			if banner != "" {
				_, _ = conn.Write([]byte(banner))
			}
			conn.Close()
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, func() { ln.Close() }
}

func TestScanFindsOnlyListeningAddressesAndReadsBanners(t *testing.T) {
	ftpPort, stopFTP := listen(t, "220 ProFTPD 1.3.6 Server ready\r\n")
	defer stopFTP()
	telnetPort, stopTelnet := listen(t, "")
	defer stopTelnet()

	addrs, err := ParseRange("127.0.0.1-4")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	hosts := Scan(ctx, addrs, Options{FTP: ftpPort, Telnet: telnetPort, HTTP: 1})
	if len(hosts) != 1 {
		t.Fatalf("found %d hosts, want only 127.0.0.1: %#v", len(hosts), hosts)
	}
	h := hosts[0]
	if h.IP != "127.0.0.1" || !h.FTP || !h.Telnet || h.HTTP {
		t.Fatalf("unexpected host: %#v", h)
	}
	if !strings.HasPrefix(h.Banner, "220 ProFTPD") {
		t.Fatalf("banner = %q", h.Banner)
	}
}

func TestScanStopsWhenCancelled(t *testing.T) {
	addrs, _ := ParseRange("192.168.77.0/24")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		t.Error("dialed after cancellation")
		return nil, errors.New("unreachable")
	}
	if hosts := Scan(ctx, addrs, Options{Dial: dial}); len(hosts) != 0 {
		t.Fatalf("hosts = %v", hosts)
	}
}

func TestScanSortsNumerically(t *testing.T) {
	addrs, _ := ParseRange("10.0.0.2-10")
	dial := func(_ context.Context, _, address string) (net.Conn, error) {
		host, port, _ := net.SplitHostPort(address)
		if port != "23" || (host != "10.0.0.10" && host != "10.0.0.9" && host != "10.0.0.2") {
			return nil, errors.New("closed")
		}
		c, _ := net.Pipe()
		return c, nil
	}
	hosts := Scan(context.Background(), addrs, Options{Dial: dial})
	var got []string
	for _, h := range hosts {
		got = append(got, h.IP)
	}
	if strings.Join(got, ",") != "10.0.0.2,10.0.0.9,10.0.0.10" {
		t.Fatalf("order = %v", got)
	}
}

func TestCleanBanner(t *testing.T) {
	if got := cleanBanner("220 hi\x00\x1b[31m there\r\n"); got != "220 hi[31m there" {
		t.Fatalf("cleanBanner = %q", got)
	}
	if len(cleanBanner(strings.Repeat("a", 500))) != 80 {
		t.Fatal("banner not truncated")
	}
}
