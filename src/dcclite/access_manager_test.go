package main

import (
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/config"
)

func freePort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func newTestManager(t *testing.T) *remoteManager {
	t.Helper()
	dir := t.TempDir()
	store, err := config.NewProfileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	application := app.New("test", store, dir)
	m := newRemoteManager(application, dir, "127.0.0.1:8787")
	t.Cleanup(m.Stop)
	return m
}

func httpsGet(address string) (int, error) {
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}}
	resp, err := client.Get("https://" + address + "/login")
	if err != nil {
		return 0, err
	}
	resp.Body.Close()
	return resp.StatusCode, nil
}

func TestRemoteManagerEnableDisableAndPersist(t *testing.T) {
	m := newTestManager(t)
	listen := fmt.Sprintf("127.0.0.1:%d", freePort(t))

	status, password, err := m.Apply(app.RemoteSettings{Enabled: true, Listen: listen, TLSHosts: "105.105.220.122, dcc.example.org"})
	if err != nil {
		t.Fatal(err)
	}
	if !status.Running || !status.PasswordSet || password == "" {
		t.Fatalf("status %#v password %q", status, password)
	}
	if code, err := httpsGet(listen); err != nil || code != http.StatusOK {
		t.Fatalf("login page: %d %v", code, err)
	}
	found := false
	for _, u := range status.URLs {
		if strings.Contains(u, "105.105.220.122") {
			found = true
		}
	}
	if !found {
		t.Fatalf("public address missing from %v", status.URLs)
	}

	// A second run reads the saved settings and does not generate a new password.
	again := newRemoteManager(m.application, m.dataDir, m.loopback)
	t.Cleanup(again.Stop)
	m.Stop()
	if err := again.Init(remoteSettings{}); err != nil {
		t.Fatal(err)
	}
	if st := again.Status(); !st.Running || st.Listen != listen {
		t.Fatalf("saved settings were not restored: %#v", st)
	}
	again.Stop()

	status, _, err = m.Apply(app.RemoteSettings{Enabled: false, Listen: listen})
	if err != nil || status.Running {
		t.Fatalf("disable: %#v %v", status, err)
	}
	if _, err := httpsGet(listen); err == nil {
		t.Fatal("listener still answers after being switched off")
	}
}

func TestRemoteManagerRejectsBadInputAndKeepsOldListener(t *testing.T) {
	m := newTestManager(t)
	listen := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	if _, _, err := m.Apply(app.RemoteSettings{Enabled: true, Listen: listen}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []app.RemoteSettings{
		{Enabled: true, Listen: "8788"},
		{Enabled: true, Listen: "0.0.0.0:8787"}, // the local port
		{Enabled: true, Listen: listen, TLSHosts: "bad host!"},
	} {
		if _, _, err := m.Apply(bad); err == nil {
			t.Errorf("%#v accepted", bad)
		}
	}
	if code, err := httpsGet(listen); err != nil || code != http.StatusOK {
		t.Fatalf("old listener lost after a rejected change: %d %v", code, err)
	}
}

func TestRemoteManagerPassword(t *testing.T) {
	m := newTestManager(t)
	if err := m.SetPassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := m.SetPassword("a long enough password"); err != nil {
		t.Fatal(err)
	}
	if !m.Status().PasswordSet {
		t.Fatal("password not reported as set")
	}
	if p, err := m.GeneratePassword(); err != nil || len(p) < 20 {
		t.Fatalf("generated %q %v", p, err)
	}
}

func TestRemoteManagerCommandLineControlsListener(t *testing.T) {
	m := newTestManager(t)
	listen := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	if err := m.Init(remoteSettings{Listen: listen}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); !st.Running || st.Managed {
		t.Fatalf("status %#v", st)
	}
	if _, _, err := m.Apply(app.RemoteSettings{Enabled: false, Listen: listen}); err == nil {
		t.Fatal("UI changed a listener that the command line controls")
	}
}

func TestRemoteManagerPlainHTTPLoginWorks(t *testing.T) {
	m := newTestManager(t)
	listen := fmt.Sprintf("127.0.0.1:%d", freePort(t))
	status, password, err := m.Apply(app.RemoteSettings{Enabled: true, Listen: listen, TLSHosts: "myname.ddns.net", PlainHTTP: true})
	if err != nil || !status.Running || password == "" {
		t.Fatalf("apply: %#v %q %v", status, password, err)
	}
	if status.Fingerprint != "" {
		t.Fatalf("a plain listener reported a certificate: %q", status.Fingerprint)
	}
	var link bool
	for _, u := range status.URLs {
		if !strings.HasPrefix(u, "http://") {
			t.Fatalf("link %q is not plain http", u)
		}
		link = link || strings.Contains(u, "myname.ddns.net")
	}
	if !link {
		t.Fatalf("dynamic DNS name missing from %v", status.URLs)
	}
	// A TLS client must not get through: the port speaks plain HTTP.
	if _, err := httpsGet(listen); err == nil {
		t.Fatal("HTTPS handshake succeeded on a plain HTTP listener")
	}

	client := &http.Client{Timeout: 3 * time.Second}
	base := "http://" + listen
	req, _ := http.NewRequest(http.MethodPost, base+"/api/v1/auth/login", strings.NewReader(`{"password":"`+password+`"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", base)
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("login over plain HTTP: %v %v", resp, err)
	}
	resp.Body.Close()
	cookies := resp.Cookies()
	if len(cookies) != 1 || cookies[0].Name != "dcc_session" || cookies[0].Secure {
		t.Fatalf("unexpected session cookie: %#v", cookies)
	}
	req, _ = http.NewRequest(http.MethodGet, base+"/api/v1/profiles", nil)
	req.AddCookie(cookies[0])
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("signed-in request: %v %v", resp, err)
	}
	resp.Body.Close()
	// Without the cookie the API stays closed.
	resp, err = client.Get(base + "/api/v1/profiles")
	if err != nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request: %v %v", resp, err)
	}
	resp.Body.Close()
	// A foreign origin cannot change state even with a valid session.
	req, _ = http.NewRequest(http.MethodPost, base+"/api/v1/profiles/x/test", nil)
	req.AddCookie(cookies[0])
	req.Header.Set("Origin", "http://evil.example")
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin request: %v %v", resp, err)
	}
	resp.Body.Close()
}
