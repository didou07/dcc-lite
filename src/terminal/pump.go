package terminal

import (
	"errors"
	"sync"

	"github.com/didou07/dcc-lite/src/core"
)

const replayLimit = 512 << 10

var ErrDetached = errors.New("terminal attachment closed")

type outputPump struct {
	backend core.TerminalSession

	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	base int64
	err  error
}

func newOutputPump(backend core.TerminalSession) *outputPump {
	p := &outputPump{backend: backend}
	p.cond = sync.NewCond(&p.mu)
	go p.run()
	return p
}

func (p *outputPump) run() {
	chunk := make([]byte, 4096)
	for {
		n, err := p.backend.Read(chunk)
		p.mu.Lock()
		if n > 0 {
			p.buf = append(p.buf, chunk[:n]...)

			if len(p.buf) > replayLimit+replayLimit/2 {
				over := len(p.buf) - replayLimit
				p.buf = append([]byte(nil), p.buf[over:]...)
				p.base += int64(over)
			}
		}
		if err != nil {
			p.err = err
		}
		p.cond.Broadcast()
		p.mu.Unlock()
		if err != nil {
			return
		}
	}
}

type Attachment struct {
	pump    *outputPump
	pos     int64
	lost    int64
	closed  bool
	onClose func(*Attachment)
	once    sync.Once
}

func newAttachment(p *outputPump, offset int64, onClose func(*Attachment)) *Attachment {
	p.mu.Lock()
	defer p.mu.Unlock()
	end := p.base + int64(len(p.buf))
	a := &Attachment{pump: p, onClose: onClose}
	switch {
	case offset < 0 || offset > end:

		a.pos = p.base
	case offset < p.base:
		a.pos = p.base
		a.lost = p.base - offset
	default:
		a.pos = offset
	}
	return a
}

func (a *Attachment) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	pump := a.pump
	pump.mu.Lock()
	defer pump.mu.Unlock()
	for {
		if a.closed {
			return 0, ErrDetached
		}
		end := pump.base + int64(len(pump.buf))
		if a.pos < pump.base {
			a.lost += pump.base - a.pos
			a.pos = pump.base
		}
		if a.pos < end {
			n := copy(p, pump.buf[a.pos-pump.base:])
			a.pos += int64(n)
			return n, nil
		}
		if pump.err != nil {
			return 0, pump.err
		}
		pump.cond.Wait()
	}
}

func (a *Attachment) Position() int64 {
	a.pump.mu.Lock()
	defer a.pump.mu.Unlock()
	return a.pos
}

func (a *Attachment) Lost() int64 {
	a.pump.mu.Lock()
	defer a.pump.mu.Unlock()
	return a.lost
}

func (a *Attachment) Write(p []byte) (int, error) { return a.pump.backend.Write(p) }
func (a *Attachment) Resize(columns, rows int) error {
	return a.pump.backend.Resize(columns, rows)
}

func (a *Attachment) Close() error {
	a.once.Do(func() {
		a.pump.mu.Lock()
		a.closed = true
		a.pump.cond.Broadcast()
		a.pump.mu.Unlock()
		if a.onClose != nil {
			a.onClose(a)
		}
	})
	return nil
}

var _ core.TerminalSession = (*Attachment)(nil)
