package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/didou07/dcc-lite/src/remoteaccess"
)

func TestValidateRemoteListen(t *testing.T) {
	for _, ok := range []string{"0.0.0.0:8788", ":8788", "192.168.1.5:9443", "[::]:8788"} {
		if err := validateRemoteListen(ok, "127.0.0.1:8787"); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"8788", "0.0.0.0:0", "0.0.0.0:99999", "0.0.0.0:abc", "0.0.0.0:8787", ":8787", "127.0.0.1:8787"} {
		if err := validateRemoteListen(bad, "127.0.0.1:8787"); err == nil {
			t.Errorf("%s accepted", bad)
		}
	}
	// The same port on a different specific interface does not clash with the loopback listener.
	if err := validateRemoteListen("192.168.1.5:8787", "127.0.0.1:8787"); err != nil {
		t.Errorf("same port on another interface rejected: %v", err)
	}
}

func TestPrepareRemoteGeneratesPasswordOnlyOnce(t *testing.T) {
	dir := t.TempDir()
	settings := remoteSettings{Listen: "0.0.0.0:8788", ExtraHost: "dcc.example.org, 203.0.113.9"}
	first, err := prepareRemote(dir, settings)
	if err != nil {
		t.Fatal(err)
	}
	if first.NewPassword == "" || first.Certificate.Source != "self-signed" {
		t.Fatalf("first start: %#v", first)
	}
	second, err := prepareRemote(dir, settings)
	if err != nil {
		t.Fatal(err)
	}
	if second.NewPassword != "" {
		t.Fatal("password was generated again on the second start")
	}
	if second.Certificate.Fingerprint != first.Certificate.Fingerprint {
		t.Fatal("certificate changed although nothing did")
	}
	joined := strings.Join(first.Certificate.Hosts, " ")
	if !strings.Contains(joined, "dcc.example.org") || !strings.Contains(joined, "203.0.113.9") || !strings.Contains(joined, "localhost") {
		t.Fatalf("certificate hosts = %v", first.Certificate.Hosts)
	}
}

func TestPrepareRemoteRequiresCertAndKeyTogether(t *testing.T) {
	if _, err := prepareRemote(t.TempDir(), remoteSettings{Listen: ":8788", CertFile: "cert.pem"}); err == nil {
		t.Fatal("certificate without key accepted")
	}
	if _, err := prepareRemote(t.TempDir(), remoteSettings{Listen: ":8788", CertFile: "/missing/cert.pem", KeyFile: "/missing/key.pem"}); err == nil {
		t.Fatal("missing certificate files accepted")
	}
}

func TestManageRemotePassword(t *testing.T) {
	dir := t.TempDir()
	var out bytes.Buffer
	handled, err := manageRemotePassword(dir, true, false, strings.NewReader("my chosen password\n"), &out)
	if err != nil || !handled {
		t.Fatalf("set: handled=%v err=%v", handled, err)
	}
	creds, _ := remoteaccess.OpenCredentials(dir)
	if !creds.Verify("my chosen password") {
		t.Fatal("chosen password not stored")
	}
	if strings.Contains(out.String(), "my chosen password") {
		t.Fatal("password echoed to output")
	}

	if _, err := manageRemotePassword(dir, true, false, strings.NewReader("short\n"), &out); err == nil {
		t.Fatal("short password accepted")
	}
	out.Reset()
	if _, err := manageRemotePassword(dir, false, true, nil, &out); err != nil {
		t.Fatal(err)
	}
	generated := strings.TrimSpace(out.String()[strings.LastIndex(out.String(), ": ")+2:])
	creds, _ = remoteaccess.OpenCredentials(dir)
	if !creds.Verify(generated) || creds.Verify("my chosen password") {
		t.Fatalf("reset did not replace the password (printed %q)", generated)
	}
	if handled, err := manageRemotePassword(dir, false, false, nil, &out); handled || err != nil {
		t.Fatal("no flags must be a no-op")
	}
	if _, err := manageRemotePassword(dir, true, true, nil, &out); err == nil {
		t.Fatal("conflicting flags accepted")
	}
}

func TestDisplayHostsSkipsLoopback(t *testing.T) {
	got := displayHosts([]string{"localhost", "127.0.0.1", "::1", "fe80::1", "vubox", "192.168.1.5"})
	if strings.Join(got, ",") != "vubox,192.168.1.5" {
		t.Fatalf("displayHosts = %v", got)
	}
}
