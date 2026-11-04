package httpapi

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/didou07/dcc-lite/src/config"
)

func profileFor(t *testing.T, srv *httptest.Server, scheme string, insecure bool) config.Profile {
	t.Helper()
	host, port, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(port)
	return config.Profile{Host: host, Port: n, Scheme: scheme, InsecureTLS: insecure}
}

// Old receivers only offer RSA key exchange, which Go's default client refuses.
func TestDeviceWebHTTPSReachesReceiverWithOldCipherSuites(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }))
	srv.TLS = &tls.Config{MaxVersion: tls.VersionTLS12, CipherSuites: []uint16{tls.TLS_RSA_WITH_AES_128_GCM_SHA256}}
	srv.StartTLS()
	defer srv.Close()

	web := newDeviceWeb(profileFor(t, srv, "https", true), 5*time.Second)
	resp, err := web.get(context.Background(), "/grab")
	if err != nil {
		t.Fatalf("HTTPS device with RSA-only ciphers unreachable: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

func TestDeviceWebUnreachableExplainsHTTPSOnPlainPort(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	web := newDeviceWeb(profileFor(t, srv, "https", true), 3*time.Second)
	_, err := web.get(context.Background(), "/grab")
	if err == nil {
		t.Fatal("expected an error")
	}
	if msg := web.unreachable(err); !strings.Contains(msg, "plain HTTP") || !strings.Contains(msg, "https://") {
		t.Fatalf("message does not explain the problem: %s", msg)
	}
	if msg := web.unreachable(errors.New("dial tcp: connection refused")); !strings.Contains(msg, "Nothing is listening") {
		t.Fatalf("refused message: %s", msg)
	}
}

func TestDeviceWebPortFollowsScheme(t *testing.T) {
	cases := []struct {
		scheme string
		port   int
		want   string
	}{
		{"https", 80, "https://192.168.1.5:443"},
		{"https", 0, "https://192.168.1.5:443"},
		{"https", 8443, "https://192.168.1.5:8443"},
		{"", 443, "http://192.168.1.5:80"},
		{"", 0, "http://192.168.1.5:80"},
		{"", 8080, "http://192.168.1.5:8080"},
	}
	for _, c := range cases {
		web := newDeviceWeb(config.Profile{Host: "192.168.1.5", Port: c.port, Scheme: c.scheme}, time.Second)
		if web.base != c.want {
			t.Errorf("scheme %q port %d: base = %s, want %s", c.scheme, c.port, web.base, c.want)
		}
	}
}
