package crashlog

import (
	"path/filepath"
	"testing"
)

func TestDir(t *testing.T) {
	if got := Dir([]string{"--log-dir", "/x/l"}); got != "/x/l" {
		t.Fatal(got)
	}
	if got := Dir([]string{"-data-dir=/d"}); got != filepath.Join("/d", "logs") {
		t.Fatal(got)
	}
	if got := Dir(nil); filepath.Base(got) != "logs" && filepath.Base(got) != "DCCLite-logs" {
		t.Fatal(got)
	}
}
