package filetransfer

import (
	"context"
	"sync/atomic"
	"time"
)

var rateLimitBytesPerSecond atomic.Int64

const MaxRateLimitKBps = 100 * 1024

func SetRateLimitKBps(kbps int64) {
	if kbps < 0 {
		kbps = 0
	}
	if kbps > MaxRateLimitKBps {
		kbps = MaxRateLimitKBps
	}
	rateLimitBytesPerSecond.Store(kbps * 1024)
}

func RateLimitKBps() int64 { return rateLimitBytesPerSecond.Load() / 1024 }

type pacer struct {
	ctx  context.Context
	next time.Time
}

func newPacer(ctx context.Context) *pacer { return &pacer{ctx: ctx} }

func (p *pacer) wait(n int) {
	if p == nil || n <= 0 {
		return
	}
	limit := rateLimitBytesPerSecond.Load()
	if limit <= 0 {
		p.next = time.Time{}
		return
	}
	now := time.Now()
	if p.next.Before(now) {
		p.next = now
	}
	p.next = p.next.Add(time.Duration(int64(n)) * time.Second / time.Duration(limit))
	delay := p.next.Sub(now) - 250*time.Millisecond
	if delay <= 0 {
		return
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-p.ctx.Done():
	}
}
