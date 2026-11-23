package logx

import (
	"strings"
	"testing"
)

func TestRedactHidesSecrets(t *testing.T) {
	cases := map[string][]string{ // input -> fragments that must be gone
		`login password=hunter2 ok`:                       {"hunter2"},
		`password: "my secret phrase"`:                    {"my secret phrase"},
		`TOKEN='abc123xyz'`:                               {"abc123xyz"},
		`ftp://root:dreambox@192.168.1.5/etc`:             {"dreambox"},
		`GET /api?user=a&password=pw123&x=1`:              {"pw123"},
		`Authorization: Bearer-abcdef`:                    {"Bearer-abcdef"},
		`Authorization: Bearer abc123token`:               {"abc123token"},
		`Authorization: Basic dXNlcjpwYXNz`:               {"dXNlcjpwYXNz"},
		`Cookie: __Host-dcc_session=AAAABBBB; theme=dark`: {"AAAABBBB"},
		`api_key=K1 secret=S2 client_secret=C3 passwd=P4`: {"K1", "S2", "C3", "P4"},
	}
	for in, gone := range cases {
		out := Redact(in)
		for _, g := range gone {
			if strings.Contains(out, g) {
				t.Errorf("Redact(%q) = %q, still contains %q", in, out, g)
			}
		}
	}
}

func TestRedactKeepsHarmlessText(t *testing.T) {
	for _, in := range []string{"", "file transfer finished", "bypass=1 compass=north", "tokenizer ready"} {
		if got := Redact(in); got != in {
			t.Errorf("Redact(%q) changed it to %q", in, got)
		}
	}
}

func TestSanitizerMasksPersonalData(t *testing.T) {
	s := NewSanitizer(Term{Value: "Living Room Box", Kind: "device"}, Term{Value: "alice", Kind: "user"})
	in := "Living Room Box at 192.168.1.20 mac aa:bb:cc:dd:ee:ff mail bob@example.com user Alice password=pw"
	out := s.Text(in)
	for _, leak := range []string{"Living Room", "192.168.1.20", "aa:bb:cc", "bob@example.com", "Alice", "=pw"} {
		if strings.Contains(out, leak) {
			t.Errorf("%q leaked in %q", leak, out)
		}
	}
	if !strings.Contains(out, "device#1") {
		t.Errorf("device label missing: %q", out)
	}
}

func TestSanitizerLabelsAreStableAndDistinct(t *testing.T) {
	s := NewSanitizer()
	a := s.Text("from 192.168.1.20 to 192.168.1.21 and again 192.168.1.20")
	parts := strings.Fields(a)
	if parts[1] != parts[len(parts)-1] || parts[1] == parts[3] {
		t.Fatalf("labels not stable/distinct: %q", a)
	}
}

func TestSanitizerKeepsLoopbackAndIgnoresShortOrGenericTerms(t *testing.T) {
	s := NewSanitizer(Term{Value: "ab", Kind: "x"}, Term{Value: "root", Kind: "user"}, Term{Value: "localhost", Kind: "host"})
	in := "listening on 127.0.0.1 as root on localhost ab"
	if got := s.Text(in); got != in {
		t.Fatalf("over-redacted: %q", got)
	}
}
