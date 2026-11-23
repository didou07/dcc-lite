package updater

import "testing"

func TestParseVersion(t *testing.T) {
	ok := []string{"1.2.3", "v1.2.3", " 1.2.3 ", "1.2.3-beta.1", "1.2.3+build5", "0.0.0"}
	bad := []string{"", "dev", "1.2", "1.2.3.4", "a.b.c", "-1.2.3", "1.2.x"}
	for _, v := range ok {
		if _, good := parseVersion(v); !good {
			t.Errorf("%q should parse", v)
		}
	}
	for _, v := range bad {
		if _, good := parseVersion(v); good {
			t.Errorf("%q should not parse", v)
		}
	}
}

func TestNewer(t *testing.T) {
	cases := []struct {
		candidate, current string
		want               bool
	}{
		{"1.1.10", "1.1.9", true},
		{"1.2.0", "1.1.9", true},
		{"2.0.0", "1.9.9", true},
		{"1.1.9", "1.1.9", false},
		{"1.1.8", "1.1.9", false},
		{"1.1.9", "1.1.9-rc1", true},
		{"1.1.9-rc1", "1.1.9", false},
		{"1.1.9-rc2", "1.1.9-rc1", true},
		{"junk", "1.1.9", false},
		{"1.1.9", "dev", false},
	}
	for _, c := range cases {
		if got := newer(c.candidate, c.current); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.candidate, c.current, got, c.want)
		}
	}
}
