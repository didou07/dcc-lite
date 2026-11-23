package connection

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/didou07/dcc-lite/src/config"
)

func listen(t *testing.T) (net.Listener, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("cannot listen on loopback: %v", err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	return l, l.Addr().(*net.TCPAddr).Port
}

// freePort returns a port nobody listens on.
func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skip(err)
	}
	p := l.Addr().(*net.TCPAddr).Port
	l.Close()
	return p
}

func profile(web, ftp, telnet int) config.Profile {
	return config.Profile{ID: "profile-1", Name: "box", Host: "127.0.0.1", Username: "root", Port: web, FTPPort: ftp, TelnetPort: telnet, Enabled: true}
}

func TestTCPCheckerOnlinePartialOffline(t *testing.T) {
	c := &TCPChecker{Timeout: time.Second}
	l1, p1 := listen(t)
	_, p2 := listen(t)
	l3, p3 := listen(t)

	st := c.Check(context.Background(), profile(p1, p2, p3))
	if st.State != "online" || len(st.Services) != 3 || st.CheckedAt.IsZero() {
		t.Fatalf("online: %+v", st)
	}
	for _, s := range st.Services {
		if !s.Open {
			t.Errorf("%s should be open", s.Name)
		}
	}

	l3.Close() // Telnet goes away
	st = c.Check(context.Background(), profile(p1, p2, p3))
	if st.State != "partial" || !strings.Contains(st.Message, "WebIf") || strings.Contains(st.Message, "Telnet") {
		t.Fatalf("partial: %+v", st)
	}
	if st.Services[2].Open || st.Services[2].Error == "" {
		t.Errorf("telnet result = %+v", st.Services[2])
	}

	l1.Close()
	st = c.Check(context.Background(), profile(freePort(t), freePort(t), freePort(t)))
	if st.State != "offline" || st.Message != "Connection refused" {
		t.Fatalf("offline: %+v", st)
	}
}

func TestTCPCheckerRejectsInvalidProfile(t *testing.T) {
	st := (&TCPChecker{}).Check(context.Background(), config.Profile{ID: "x", Name: "", Host: "127.0.0.1"})
	if st.State != "error" || st.Message == "" || len(st.Services) != 0 {
		t.Fatalf("%+v", st)
	}
	st = (&TCPChecker{}).Check(context.Background(), config.Profile{ID: "x", Name: "n", Username: "root", Host: "bad host/path"})
	if st.State != "error" {
		t.Fatalf("%+v", st)
	}
}

func TestTCPCheckerReportsCancellation(t *testing.T) {
	_, p := listen(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	st := (&TCPChecker{Timeout: time.Second}).Check(ctx, profile(p, p, p))
	if st.State != "offline" || st.Message != "Check was cancelled" {
		t.Fatalf("%+v", st)
	}
}

func TestClassifyNetworkError(t *testing.T) {
	for msg, want := range map[string]string{
		"dial tcp: lookup nope: no such host":              "Host name could not be resolved",
		"dial tcp 1.2.3.4:80: connect: connection refused": "Connection refused",
		"connect: network is unreachable":                  "Network is unreachable",
		"i/o timeout":                                      "Connection timed out",
		"context deadline exceeded":                        "Connection timed out",
		"operation was canceled":                           "Cancelled",
		"something odd":                                    "Connection failed",
	} {
		if got := classifyNetworkError(errors.New(msg)); got != want {
			t.Errorf("%q -> %q, want %q", msg, got, want)
		}
	}
}

type fakeChecker struct {
	mu      sync.Mutex
	calls   int
	started chan int // receives the call number when a check begins
	fn      func(ctx context.Context, n int, p config.Profile) Status
}

func (f *fakeChecker) Check(ctx context.Context, p config.Profile) Status {
	f.mu.Lock()
	f.calls++
	n := f.calls
	f.mu.Unlock()
	if f.started != nil {
		f.started <- n
	}
	return f.fn(ctx, n, p)
}

func TestManagerStoresResultAndLists(t *testing.T) {
	m := NewManager(&fakeChecker{fn: func(_ context.Context, _ int, p config.Profile) Status {
		return Status{ProfileID: p.ID, State: "online", Message: "ok"}
	}})
	if _, ok := m.Get("profile-1"); ok {
		t.Fatal("status before any check")
	}
	if st := m.Test(context.Background(), profile(1, 2, 3)); st.State != "online" {
		t.Fatalf("%+v", st)
	}
	got, ok := m.Get("profile-1")
	if !ok || got.State != "online" || len(m.List()) != 1 {
		t.Fatalf("get=%+v ok=%v list=%v", got, ok, m.List())
	}
	m.Forget("profile-1")
	if _, ok := m.Get("profile-1"); ok || len(m.List()) != 0 {
		t.Fatal("Forget did not clear the status")
	}
}

func TestManagerShowsCheckingWhileRunning(t *testing.T) {
	release := make(chan struct{})
	fc := &fakeChecker{started: make(chan int, 1), fn: func(_ context.Context, _ int, p config.Profile) Status {
		<-release
		return Status{ProfileID: p.ID, State: "online"}
	}}
	m := NewManager(fc)
	done := make(chan struct{})
	go func() { m.Test(context.Background(), profile(1, 2, 3)); close(done) }()
	<-fc.started
	if st, _ := m.Get("profile-1"); st.State != "checking" {
		t.Fatalf("state during check = %q", st.State)
	}
	close(release)
	<-done
	if st, _ := m.Get("profile-1"); st.State != "online" {
		t.Fatalf("state after check = %q", st.State)
	}
}

func TestNewerCheckSupersedesOlderOne(t *testing.T) {
	fc := &fakeChecker{started: make(chan int, 2)}
	fc.fn = func(ctx context.Context, n int, p config.Profile) Status {
		if n == 1 { // slow check: waits until the manager cancels it
			<-ctx.Done()
			return Status{ProfileID: p.ID, State: "offline", Message: "stale"}
		}
		return Status{ProfileID: p.ID, State: "online", Message: "fresh"}
	}
	m := NewManager(fc)
	first := make(chan Status, 1)
	go func() { first <- m.Test(context.Background(), profile(1, 2, 3)) }()
	<-fc.started
	if st := m.Test(context.Background(), profile(1, 2, 3)); st.Message != "fresh" {
		t.Fatalf("second = %+v", st)
	}
	select {
	case st := <-first:
		if st.Message != "stale" {
			t.Fatalf("first = %+v", st)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("first check was not cancelled")
	}
	if st, _ := m.Get("profile-1"); st.Message != "fresh" {
		t.Fatalf("stale result overwrote the newer one: %+v", st)
	}
}

func TestForgetDuringCheckDiscardsResult(t *testing.T) {
	fc := &fakeChecker{started: make(chan int, 1)}
	fc.fn = func(ctx context.Context, _ int, p config.Profile) Status {
		<-ctx.Done()
		return Status{ProfileID: p.ID, State: "offline"}
	}
	m := NewManager(fc)
	done := make(chan struct{})
	go func() { m.Test(context.Background(), profile(1, 2, 3)); close(done) }()
	<-fc.started
	m.Forget("profile-1")
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Forget did not cancel the running check")
	}
	if _, ok := m.Get("profile-1"); ok {
		t.Fatal("a forgotten device came back after its check finished")
	}
}

func TestManagerKeepsDevicesIndependent(t *testing.T) {
	m := NewManager(&fakeChecker{fn: func(_ context.Context, _ int, p config.Profile) Status {
		return Status{ProfileID: p.ID, State: "online"}
	}})
	a, b := profile(1, 2, 3), profile(1, 2, 3)
	a.ID, b.ID = "device-a", "device-b"
	m.Test(context.Background(), a)
	m.Test(context.Background(), b)
	m.Forget("device-a")
	if _, ok := m.Get("device-b"); !ok || len(m.List()) != 1 {
		t.Fatal("forgetting one device affected another")
	}
}
