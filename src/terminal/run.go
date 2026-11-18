package terminal

import (
	"bytes"
	"context"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	// ErrNoShell means the receiver accepted the connection but no shell prompt came
	// (wrong user name or password, or something other than a shell answered).
	ErrNoShell = errors.New("no shell prompt over Telnet")
	// ErrCommandTimeout means the command did not finish in time; it may still be running there.
	ErrCommandTimeout = errors.New("the command did not finish in time")
)

const (
	runBeginMark = "__DCC_BEGIN__"
	runEndPrefix = "__DCC_END_"
)

var (
	runEndPattern = regexp.MustCompile(`__DCC_END_(\d+)__`)
	ansiPattern   = regexp.MustCompile(`\x1b\[[0-9;?]*[A-Za-z]|\x1b[()][A-Z0-9]|\x1b[=>]`)
)

func RunCommand(ctx context.Context, host string, port int, username, password, command string, maxOutput int) (string, int, error) {
	if command == "" || len(command) > 6000 || strings.ContainsAny(command, "\r\n\x00") {
		return "", -1, errors.New("invalid command")
	}
	if maxOutput <= 0 {
		maxOutput = 4 << 20
	}
	address := net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
	conn, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, "tcp", address)
	if err != nil {
		return "", -1, err
	}
	session := NewTelnetSession(conn, 200, 50, username, password)
	defer session.Close()
	line := "printf '__DCC_B%s__\\n' EGIN; " + command + "; printf '__DCC_E%s_%s__\\n' ND $?\r"
	if err := session.SetInitialCommand([]byte(line)); err != nil {
		return "", -1, err
	}

	type result struct {
		out  string
		code int
		err  error
	}
	done := make(chan result, 1)
	go func() {
		var acc bytes.Buffer
		buf := make([]byte, 8192)
		for {
			n, rerr := session.Read(buf)
			if n > 0 {
				if acc.Len()+n > maxOutput {
					done <- result{"", -1, errors.New("the receiver sent too much output")}
					return
				}
				acc.Write(buf[:n])
				tail := acc.Bytes()
				if len(tail) > 64 {
					tail = tail[len(tail)-64:]
				}
				if m := runEndPattern.FindSubmatch(tail); m != nil {
					code, _ := strconv.Atoi(string(m[1]))
					done <- result{cleanRunOutput(acc.String()), code, nil}
					return
				}
			}
			if rerr != nil {
				text := acc.String()
				if !strings.Contains(text, runBeginMark) {
					done <- result{"", -1, ErrNoShell}
				} else {
					done <- result{cleanRunOutput(text), -1, rerr}
				}
				return
			}
		}
	}()

	select {
	case r := <-done:
		_, _ = session.Write([]byte("exit\r"))
		return r.out, r.code, r.err
	case <-ctx.Done():
		_ = session.Close()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", -1, ErrCommandTimeout
		}
		return "", -1, ctx.Err()
	}
}

func cleanRunOutput(text string) string {
	text = ansiPattern.ReplaceAllString(text, "")
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "")
	if i := strings.Index(text, runBeginMark); i >= 0 {
		text = text[i+len(runBeginMark):]
		text = strings.TrimPrefix(text, "\n")
	} else {
		return ""
	}
	if i := strings.LastIndex(text, runEndPrefix); i >= 0 {
		text = text[:i]
	}
	return text
}
