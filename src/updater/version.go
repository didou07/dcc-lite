package updater

import (
	"strconv"
	"strings"
)

type semver struct {
	major, minor, patch int
	pre                 string
}

func parseVersion(v string) (semver, bool) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	var s semver
	if i := strings.IndexByte(v, '-'); i >= 0 {
		s.pre = v[i+1:]
		v = v[:i]
	}
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return s, false
	}
	nums := [3]*int{&s.major, &s.minor, &s.patch}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return s, false
		}
		*nums[i] = n
	}
	return s, true
}

// newer reports whether candidate is a strictly newer version than current.
func newer(candidate, current string) bool {
	c, ok1 := parseVersion(candidate)
	cur, ok2 := parseVersion(current)
	if !ok1 || !ok2 {
		return false
	}
	switch {
	case c.major != cur.major:
		return c.major > cur.major
	case c.minor != cur.minor:
		return c.minor > cur.minor
	case c.patch != cur.patch:
		return c.patch > cur.patch
	}
	if c.pre == cur.pre {
		return false
	}
	if c.pre == "" { // release beats its own pre-release
		return true
	}
	if cur.pre == "" {
		return false
	}
	return c.pre > cur.pre
}
