package httpapi

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/config"
)

func TestTerminalWebSocketTelnetRoundTrip(t *testing.T) {
	remote, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer remote.Close()
	remoteResult := make(chan string, 1)
	go func() {
		conn, acceptErr := remote.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		_, _ = conn.Write([]byte("Welcome\r\n"))
		input := make([]byte, len("echo DCC\r"))
		if _, readErr := io.ReadFull(conn, input); readErr == nil {
			remoteResult <- string(input)
		}
	}()

	root := t.TempDir()
	profiles, err := config.NewProfileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	application := app.New("test", profiles, root)
	t.Cleanup(application.Terminals.CloseAll)
	profile, err := profiles.Save(config.Profile{Name: "Test receiver", Host: "127.0.0.1", Username: "root", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(New(application))
	defer server.Close()
	baseURL := server.URL
	payload, _ := json.Marshal(map[string]any{"profile_id": profile.ID, "protocol": "telnet", "port": remote.Addr().(*net.TCPAddr).Port, "columns": 100, "rows": 30})
	request, _ := http.NewRequest(http.MethodPost, baseURL+"/api/v1/terminal/sessions", bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", baseURL)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		data, _ := io.ReadAll(response.Body)
		t.Fatalf("terminal open returned %d: %s", response.StatusCode, data)
	}
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
		WebSocket string `json:"websocket"`
	}
	if err := json.NewDecoder(response.Body).Decode(&created); err != nil {
		t.Fatal(err)
	}
	if created.Session.ID == "" {
		t.Fatal("missing terminal session ID")
	}

	address := strings.TrimPrefix(baseURL, "http://")
	// A browser terminal handshake must carry a matching loopback Origin.
	missingOriginConn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	var rejectedNonce [16]byte
	if _, err := rand.Read(rejectedNonce[:]); err != nil {
		t.Fatal(err)
	}
	_, err = fmt.Fprintf(missingOriginConn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\n\r\n", created.WebSocket, address, base64.StdEncoding.EncodeToString(rejectedNonce[:]))
	if err != nil {
		t.Fatal(err)
	}
	missingResponse, err := http.ReadResponse(bufio.NewReader(missingOriginConn), &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if missingResponse.StatusCode != http.StatusForbidden {
		t.Fatalf("missing Origin should be rejected, got %d", missingResponse.StatusCode)
	}
	_ = missingResponse.Body.Close()
	_ = missingOriginConn.Close()

	conn, err := net.DialTimeout("tcp", address, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		t.Fatal(err)
	}
	key := base64.StdEncoding.EncodeToString(nonce[:])
	_, err = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: %s\r\nOrigin: %s\r\n\r\n", created.WebSocket, address, key, baseURL)
	if err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	upgrade, err := http.ReadResponse(reader, &http.Request{Method: http.MethodGet})
	if err != nil {
		t.Fatal(err)
	}
	if upgrade.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("WebSocket upgrade returned %d", upgrade.StatusCode)
	}

	frame, err := readServerWebSocketFrame(reader)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Type string `json:"type"`
		Data string `json:"data"`
	}
	if err := json.Unmarshal(frame, &output); err != nil {
		t.Fatal(err)
	}
	decoded, decodeErr := base64.StdEncoding.DecodeString(output.Data)
	if decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if output.Type != "output" || !strings.Contains(string(decoded), "Welcome") {
		t.Fatalf("unexpected terminal output: %+v", output)
	}

	// A browser terminal must forward every key immediately, not wait for a
	// separate command field or a form submission.
	for _, character := range []string{"e", "c", "h", "o", " ", "D", "C", "C", "\r"} {
		payload, marshalErr := json.Marshal(map[string]string{"type": "input", "data": character})
		if marshalErr != nil {
			t.Fatal(marshalErr)
		}
		writeClientWebSocketFrame(t, conn, payload)
	}
	select {
	case value := <-remoteResult:
		if value != "echo DCC\r" {
			t.Fatalf("remote got %q, want direct key stream %q", value, "echo DCC\r")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("remote Telnet server did not receive terminal input")
	}
}

func writeClientWebSocketFrame(t *testing.T, conn net.Conn, payload []byte) {
	t.Helper()
	mask := [4]byte{1, 2, 3, 4}
	var header []byte
	header = append(header, 0x81)
	if len(payload) < 126 {
		header = append(header, 0x80|byte(len(payload)))
	} else {
		header = append(header, 0x80|126, byte(len(payload)>>8), byte(len(payload)))
	}
	header = append(header, mask[:]...)
	masked := append([]byte(nil), payload...)
	for i := range masked {
		masked[i] ^= mask[i&3]
	}
	if _, err := conn.Write(append(header, masked...)); err != nil {
		t.Fatal(err)
	}
}

func readServerWebSocketFrame(reader *bufio.Reader) ([]byte, error) {
	first, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	second, err := reader.ReadByte()
	if err != nil {
		return nil, err
	}
	if first&0x80 == 0 {
		return nil, fmt.Errorf("fragmented frame in test")
	}
	length := uint64(second & 0x7f)
	if length == 126 {
		var b [2]byte
		if _, err := io.ReadFull(reader, b[:]); err != nil {
			return nil, err
		}
		length = uint64(binary.BigEndian.Uint16(b[:]))
	} else if length == 127 {
		var b [8]byte
		if _, err := io.ReadFull(reader, b[:]); err != nil {
			return nil, err
		}
		length = binary.BigEndian.Uint64(b[:])
	}
	if length > 64<<10 {
		return nil, fmt.Errorf("frame too large")
	}
	payload := make([]byte, int(length))
	_, err = io.ReadFull(reader, payload)
	return payload, err
}

func TestValidWebSocketOrigin(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/ws", nil)
	request.Host = "127.0.0.1:8787"
	if validWebSocketOrigin(request) {
		t.Fatal("missing Origin should be rejected")
	}
	request.Header.Set("Origin", "https://evil.example")
	if validWebSocketOrigin(request) {
		t.Fatal("remote Origin should be rejected")
	}
	request.Header.Set("Origin", "http://localhost:8787")
	if validWebSocketOrigin(request) {
		t.Fatal("different loopback authority should be rejected")
	}
	request.Header.Set("Origin", "http://127.0.0.1:8787")
	if !validWebSocketOrigin(request) {
		t.Fatal("matching loopback Origin should be accepted")
	}
}

func TestWebSocketReadFrameRejectsUnmaskedAndNonCanonicalFrames(t *testing.T) {
	tests := []struct {
		name  string
		frame []byte
	}{
		{name: "unmasked", frame: []byte{0x81, 0x01, 'x'}},
		{name: "non-canonical 16-bit length", frame: []byte{0x81, 0xfe, 0x00, 0x01, 1, 2, 3, 4, 'x' ^ 1}},
		{name: "fragmented ping", frame: []byte{0x09, 0x80, 1, 2, 3, 4}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server, client := net.Pipe()
			defer server.Close()
			defer client.Close()
			go func() { _, _ = client.Write(test.frame) }()
			ws := &wsConn{conn: server, read: bufio.NewReader(server)}
			if _, _, _, err := ws.readFrame(); err == nil {
				t.Fatal("malformed frame was accepted")
			}
		})
	}
}

func TestWebSocketReadMessageRejectsBinaryFrames(t *testing.T) {
	server, client := net.Pipe()
	defer server.Close()
	defer client.Close()
	payload := []byte(`{"type":"input"}`)
	mask := [4]byte{1, 2, 3, 4}
	frame := []byte{0x82, 0x80 | byte(len(payload))}
	frame = append(frame, mask[:]...)
	for i, value := range payload {
		frame = append(frame, value^mask[i&3])
	}
	go func() { _, _ = client.Write(frame) }()
	ws := &wsConn{conn: server, read: bufio.NewReader(server)}
	if _, err := ws.readMessage(); err == nil || !strings.Contains(err.Error(), "binary") {
		t.Fatalf("expected binary frame rejection, got %v", err)
	}
}

func TestOpenTerminalRejectsMultilineInitialCommand(t *testing.T) {
	root := t.TempDir()
	profiles, err := config.NewProfileStore(root)
	if err != nil {
		t.Fatal(err)
	}
	application := app.New("test", profiles, root)
	t.Cleanup(application.Terminals.CloseAll)
	profile, err := profiles.Save(config.Profile{Name: "Test receiver", Host: "127.0.0.1", Username: "root", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{
		"profile_id": profile.ID, "protocol": "telnet", "port": 23,
		"initial_directory": "/tmp", "initial_command": "echo safe\nreboot",
	})
	response := request(t, New(application), http.MethodPost, "/api/v1/terminal/sessions", payload)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("multiline initial command returned %d: %s", response.Code, response.Body.String())
	}
}
