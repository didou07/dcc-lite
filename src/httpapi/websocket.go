package httpapi

import (
	"bufio"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	websocketGUID    = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
	maxWebSocketSize = 64 << 10
)

type wsConn struct {
	conn net.Conn
	read *bufio.Reader
	mu   sync.Mutex
}

func upgradeWebSocket(w http.ResponseWriter, r *http.Request) (*wsConn, error) {
	if r.Method != http.MethodGet || !headerHasToken(r.Header, "Connection", "upgrade") || !strings.EqualFold(r.Header.Get("Upgrade"), "websocket") {
		return nil, errors.New("invalid WebSocket upgrade request")
	}
	if r.Header.Get("Sec-WebSocket-Version") != "13" {
		return nil, errors.New("unsupported WebSocket version")
	}
	key := strings.TrimSpace(r.Header.Get("Sec-WebSocket-Key"))
	decoded, err := base64.StdEncoding.DecodeString(key)
	if err != nil || len(decoded) != 16 {
		return nil, errors.New("invalid WebSocket key")
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		return nil, errors.New("HTTP server does not support protocol upgrades")
	}
	conn, rw, err := hijacker.Hijack()
	if err != nil {
		return nil, err
	}
	sum := sha1.Sum([]byte(key + websocketGUID))
	accept := base64.StdEncoding.EncodeToString(sum[:])
	if _, err := fmt.Fprintf(rw, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: %s\r\n\r\n", accept); err != nil {
		_ = conn.Close()
		return nil, err
	}
	if err := rw.Flush(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	_ = conn.SetDeadline(time.Time{})
	return &wsConn{conn: conn, read: rw.Reader}, nil
}

func headerHasToken(header http.Header, name, wanted string) bool {
	for _, value := range header.Values(name) {
		for _, token := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(token), wanted) {
				return true
			}
		}
	}
	return false
}

func (c *wsConn) ReadJSON(dst any) error {
	payload, err := c.readMessage()
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(strings.NewReader(string(payload)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("WebSocket message must contain one JSON object")
	}
	return nil
}

func (c *wsConn) WriteJSON(value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.writeFrame(0x1, data)
}

func (c *wsConn) writePing() error { return c.writeFrame(0x9, nil) }
func (c *wsConn) Close() error     { _ = c.writeFrame(0x8, []byte{0x03, 0xE8}); return c.conn.Close() }

func (c *wsConn) readMessage() ([]byte, error) {
	var message []byte
	initialOpcode := byte(0)
	for {
		_ = c.conn.SetReadDeadline(time.Now().Add(75 * time.Second))
		fin, opcode, payload, err := c.readFrame()
		if err != nil {
			return nil, err
		}
		switch opcode {
		case 0x8:
			_ = c.writeFrame(0x8, payload)
			return nil, io.EOF
		case 0x9:
			if err := c.writeFrame(0xA, payload); err != nil {
				return nil, err
			}
			continue
		case 0xA:
			continue
		case 0x1:
			if initialOpcode != 0 {
				return nil, errors.New("unexpected nested WebSocket data frame")
			}
			initialOpcode = opcode
		case 0x2:
			return nil, errors.New("binary WebSocket messages are not supported")
		case 0x0:
			if initialOpcode == 0 {
				return nil, errors.New("unexpected WebSocket continuation frame")
			}
		default:
			return nil, errors.New("unsupported WebSocket opcode")
		}
		if len(message)+len(payload) > maxWebSocketSize {
			return nil, errors.New("WebSocket message exceeds size limit")
		}
		message = append(message, payload...)
		if fin {
			return message, nil
		}
	}
}

func (c *wsConn) readFrame() (bool, byte, []byte, error) {
	first, err := c.read.ReadByte()
	if err != nil {
		return false, 0, nil, err
	}
	second, err := c.read.ReadByte()
	if err != nil {
		return false, 0, nil, err
	}
	if first&0x70 != 0 {
		return false, 0, nil, errors.New("WebSocket extensions are not supported")
	}
	fin := first&0x80 != 0
	opcode := first & 0x0f
	masked := second&0x80 != 0
	if !masked {
		return false, 0, nil, errors.New("client WebSocket frame must be masked")
	}
	length := uint64(second & 0x7f)
	if length == 126 {
		var extended [2]byte
		if _, err := io.ReadFull(c.read, extended[:]); err != nil {
			return false, 0, nil, err
		}
		length = uint64(binary.BigEndian.Uint16(extended[:]))
		if length < 126 {
			return false, 0, nil, errors.New("non-canonical WebSocket payload length")
		}
	} else if length == 127 {
		var extended [8]byte
		if _, err := io.ReadFull(c.read, extended[:]); err != nil {
			return false, 0, nil, err
		}
		length = binary.BigEndian.Uint64(extended[:])
		if length < 65536 || length&(uint64(1)<<63) != 0 {
			return false, 0, nil, errors.New("invalid or non-canonical WebSocket payload length")
		}
	}
	if length > maxWebSocketSize {
		return false, 0, nil, errors.New("WebSocket frame exceeds size limit")
	}
	if opcode >= 0x8 && (!fin || length > 125) {
		return false, 0, nil, errors.New("invalid WebSocket control frame")
	}
	var mask [4]byte
	if _, err := io.ReadFull(c.read, mask[:]); err != nil {
		return false, 0, nil, err
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(c.read, payload); err != nil {
		return false, 0, nil, err
	}
	for i := range payload {
		payload[i] ^= mask[i&3]
	}
	return fin, opcode, payload, nil
}

func (c *wsConn) writeFrame(opcode byte, payload []byte) error {
	if len(payload) > maxWebSocketSize {
		return errors.New("WebSocket frame exceeds size limit")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	var header [10]byte
	header[0] = 0x80 | opcode
	headerLen := 2
	switch {
	case len(payload) < 126:
		header[1] = byte(len(payload))
	case len(payload) <= 65535:
		header[1] = 126
		binary.BigEndian.PutUint16(header[2:4], uint16(len(payload)))
		headerLen = 4
	default:
		header[1] = 127
		binary.BigEndian.PutUint64(header[2:10], uint64(len(payload)))
		headerLen = 10
	}
	if err := writeSocketAll(c.conn, header[:headerLen]); err != nil {
		return err
	}
	for len(payload) > 0 {
		n, err := c.conn.Write(payload)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		payload = payload[n:]
	}
	return nil
}

func writeSocketAll(writer io.Writer, data []byte) error {
	for len(data) > 0 {
		n, err := writer.Write(data)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
