package filetransfer

import (
	"context"
	"testing"
	"time"
)

func TestRateLimitSlowsTransferAndZeroMeansUnlimited(t *testing.T) {
	defer SetRateLimitKBps(0)

	SetRateLimitKBps(0)
	p := newPacer(context.Background())
	start := time.Now()
	for i := 0; i < 5; i++ {
		p.wait(1 << 20)
	}
	if time.Since(start) > 100*time.Millisecond {
		t.Fatalf("unlimited transfer was delayed: %v", time.Since(start))
	}

	SetRateLimitKBps(400)
	p = newPacer(context.Background())
	start = time.Now()
	for i := 0; i < 3; i++ {
		p.wait(200 * 1024)
	}
	if got := time.Since(start); got < time.Second || got > 3*time.Second {
		t.Fatalf("limited transfer took %v, want about 1.25s", got)
	}
}

func TestRateLimitStopsWhenCancelled(t *testing.T) {
	defer SetRateLimitKBps(0)
	SetRateLimitKBps(1)
	ctx, cancel := context.WithCancel(context.Background())
	p := newPacer(ctx)
	time.AfterFunc(50*time.Millisecond, cancel)
	start := time.Now()
	p.wait(10 << 20)
	if time.Since(start) > time.Second {
		t.Fatal("a cancelled transfer kept waiting")
	}
}
