package netscan

import (
	"strings"
	"testing"
)

func TestParseRangeAccepts(t *testing.T) {
	cases := []struct {
		in          string
		count       int
		first, last string
	}{
		{"192.168.1.10", 1, "192.168.1.10", "192.168.1.10"},
		{"192.168.1.0/24", 254, "192.168.1.1", "192.168.1.254"},
		{"192.168.1.5/32", 1, "192.168.1.5", "192.168.1.5"},
		{"10.0.0.0/22", 1022, "10.0.0.1", "10.0.3.254"},
		{"192.168.1.10-20", 11, "192.168.1.10", "192.168.1.20"},
		{" 192.168.1.250 - 192.168.2.2 ", 9, "192.168.1.250", "192.168.2.2"},
		{"192.168.1.77/24", 254, "192.168.1.1", "192.168.1.254"}, // host bits are masked off
	}
	for _, c := range cases {
		got, err := ParseRange(c.in)
		if err != nil {
			t.Errorf("%q: %v", c.in, err)
			continue
		}
		if len(got) != c.count || got[0].String() != c.first || got[len(got)-1].String() != c.last {
			t.Errorf("%q: %d addresses %v..%v, want %d %s..%s", c.in, len(got), got[0], got[len(got)-1], c.count, c.first, c.last)
		}
	}
}

func TestParseRangeRejects(t *testing.T) {
	for _, in := range []string{
		"", "   ", "hello", "8.8.8.8", "8.8.8.0/24", "1.1.1.1-1.1.1.9", // public addresses
		"192.168.0.0/16", "10.0.0.0/8", // too large
		"192.168.1.20-10", "192.168.1.1-300", "192.168.1.1-abc", // bad ranges
		"::1", "fe80::1/64", "192.168.1.0/33", // not IPv4
		"192.168.255.250-192.169.0.5", // leaves private space
	} {
		if _, err := ParseRange(in); err == nil {
			t.Errorf("%q accepted", in)
		}
	}
}

func TestParseRangeSizeCap(t *testing.T) {
	if _, err := ParseRange("10.0.0.1-10.0.10.1"); err == nil {
		t.Fatal("range above the per-scan cap accepted")
	}
}

func TestCleanBanner(t *testing.T) {
	if got := cleanBanner("  220 Welcome\x00\x1b[31m to\r\n"); strings.ContainsAny(got, "\x00\x1b\r\n") {
		t.Errorf("control characters survived: %q", got)
	}
	if got := cleanBanner(strings.Repeat("a", 500)); len(got) != 80 {
		t.Errorf("banner length = %d, want 80", len(got))
	}
}

func TestSuggestRangesAreScannable(t *testing.T) {
	for _, r := range SuggestRanges() {
		if _, err := ParseRange(r); err != nil {
			t.Errorf("suggested range %q cannot be scanned: %v", r, err)
		}
	}
}
