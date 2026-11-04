package terminal

import (
	"io"
	"net"
	"testing"
	"time"
)

func readN(t *testing.T, a *Attachment, want int) string {
	t.Helper()
	var got []byte
	buf := make([]byte, 64)
	deadline := time.After(3 * time.Second)
	for len(got) < want {
		type res struct {
			n   int
			err error
		}
		ch := make(chan res, 1)
		go func() { n, err := a.Read(buf); ch <- res{n, err} }()
		select {
		case r := <-ch:
			if r.err != nil {
				t.Fatalf("read: %v (got %q)", r.err, got)
			}
			got = append(got, buf[:r.n]...)
		case <-deadline:
			t.Fatalf("timed out waiting for %d bytes, got %q", want, got)
		}
	}
	return string(got)
}

func TestSessionSurvivesDetachAndResumesAtOffset(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	manager := NewManager()
	defer manager.CloseAll()
	backend := NewTelnetSession(client, 100, 30)
	manager.mu.Lock()
	manager.sessions["s"] = &managedSession{backend: backend, pump: newOutputPump(backend), expiresAt: time.Now().Add(time.Minute)}
	manager.mu.Unlock()

	first, _, err := manager.AttachAt("s", 0)
	if err != nil {
		t.Fatal(err)
	}
	go peer.Write([]byte("hello "))
	if got := readN(t, first, 6); got != "hello " {
		t.Fatalf("first read = %q", got)
	}
	offset := first.Position()
	if offset != 6 {
		t.Fatalf("position = %d, want 6", offset)
	}
	// The browser connection drops: only a detach, the shell keeps running.
	_ = first.Close()
	manager.mu.Lock()
	still := manager.sessions["s"] != nil && !manager.sessions["s"].attached
	manager.mu.Unlock()
	if !still {
		t.Fatal("session should stay and be detached")
	}
	go peer.Write([]byte("world"))
	time.Sleep(100 * time.Millisecond)

	second, _, err := manager.AttachAt("s", offset)
	if err != nil {
		t.Fatalf("reattach failed: %v", err)
	}
	if got := readN(t, second, 5); got != "world" {
		t.Fatalf("resumed output = %q, want only the missed %q", got, "world")
	}
	if second.Lost() != 0 {
		t.Fatalf("nothing should be lost, got %d", second.Lost())
	}
}

func TestReattachReplacesAHalfOpenAttachment(t *testing.T) {
	client, peer := net.Pipe()
	defer peer.Close()
	manager := NewManager()
	defer manager.CloseAll()
	backend := NewTelnetSession(client, 100, 30)
	manager.mu.Lock()
	manager.sessions["s"] = &managedSession{backend: backend, pump: newOutputPump(backend), expiresAt: time.Now().Add(time.Minute)}
	manager.mu.Unlock()

	old, _, err := manager.AttachAt("s", 0)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, err := manager.AttachAt("s", 0)
	if err != nil {
		t.Fatalf("a second page must take over, got %v", err)
	}
	if _, err := old.Read(make([]byte, 8)); err != ErrDetached {
		t.Fatalf("old attachment read = %v, want ErrDetached", err)
	}
	// Closing the replaced attachment must not mark the live one detached.
	manager.mu.Lock()
	attached := manager.sessions["s"].attached
	manager.mu.Unlock()
	if !attached {
		t.Fatal("live attachment was detached by the replaced one")
	}
	_ = fresh.Close()
}

func TestAttachmentDeliversBufferedOutputThenEOF(t *testing.T) {
	client, peer := net.Pipe()
	backend := NewTelnetSession(client, 100, 30)
	p := newOutputPump(backend)
	go func() { _, _ = peer.Write([]byte("bye")); _ = peer.Close() }()
	a := newAttachment(p, 0, nil)
	if got := readN(t, a, 3); got != "bye" {
		t.Fatalf("got %q", got)
	}
	if _, err := a.Read(make([]byte, 8)); err == nil || err == ErrDetached {
		t.Fatalf("want the backend's end-of-stream error, got %v", err)
	}
	_ = io.EOF
}

func TestOutputBufferIsBoundedAndReportsLostBytes(t *testing.T) {
	p := &outputPump{}
	p.base = 0
	p.buf = make([]byte, replayLimit)
	p.base = 1000 // pretend 1000 older bytes were trimmed
	a := newAttachment(p, 0, nil)
	if a.Lost() != 1000 {
		t.Fatalf("lost = %d, want 1000", a.Lost())
	}
}
