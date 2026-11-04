package terminal

import (
	"errors"
	"sync"

	"github.com/didou07/dcc-lite/src/core"
)

// replayLimit is how much recent output a session keeps for a browser that
// reconnects after a dropped WebSocket (sleep, Wi-Fi change, proxy timeout).
const replayLimit = 512 << 10

// ErrDetached is returned by a read on an attachment that was closed.
var ErrDetached = errors.New("terminal attachment closed")

// outputPump is the only reader of a terminal backend. It keeps the latest
// output so the shell outlives a dropped browser connection: a new WebSocket
// attaches and continues from the byte offset where the old one stopped.
type outputPump struct {
	backend core.TerminalSession

	mu   sync.Mutex
	cond *sync.Cond
	buf  []byte
	base int64 // absolute offset of buf[0]
	err  error // set once the backend stops producing output
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
			if over := len(p.buf) - replayLimit; over > 0 {
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

// Attachment is one browser's view of a session. Closing it detaches the
// browser; the session itself keeps running.
type Attachment struct {
	pump    *outputPump
	pos     int64
	lost    int64
	closed  bool // guarded by pump.mu
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
		// The browser is ahead of the server (it can only happen after a restart
		// of the session); start from what the server still has.
		a.pos = p.base
	case offset < p.base:
		a.pos = p.base
		a.lost = p.base - offset
	default:
		a.pos = offset
	}
	return a
}

// Read blocks until output is available. It returns ErrDetached after Close and
// the backend's error (normally io.EOF) once all buffered output was delivered.
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
		if a.pos < pump.base { // output was trimmed while this reader was slow
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

// Position is the absolute offset just after the output returned so far.
func (a *Attachment) Position() int64 {
	a.pump.mu.Lock()
	defer a.pump.mu.Unlock()
	return a.pos
}

// Lost is the number of output bytes the browser asked for that the server no
// longer had.
func (a *Attachment) Lost() int64 {
	a.pump.mu.Lock()
	defer a.pump.mu.Unlock()
	return a.lost
}

func (a *Attachment) Write(p []byte) (int, error) { return a.pump.backend.Write(p) }
func (a *Attachment) Resize(columns, rows int) error {
	return a.pump.backend.Resize(columns, rows)
}

// Close detaches this browser without ending the session.
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
