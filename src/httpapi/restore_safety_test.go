package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func (h *harness) postRestore(zipData []byte) (int, map[string]any) {
	h.t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "backup.zip")
	if err != nil {
		h.t.Fatal(err)
	}
	_, _ = fw.Write(zipData)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", h.srv.URL+h.url("/enigma2/restore"), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

// A restore replaces the whole channel list. If the receiver's current list cannot be saved
// first, nothing may be written, because the restore could not be undone.
func TestRestoreStopsWhenRestorePointCannotBeSaved(t *testing.T) {
	h := newHarness(t)
	// A regular file where the restore-points folder must go makes saving a restore point fail.
	if err := os.WriteFile(filepath.Join(h.dir, "restore-points"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	zipData := makeZip(t, map[string]string{"bouquets.tv": "#NAME Other\n", "lamedb": "eDVB services /4/\n"})
	code, out := h.postRestore(zipData)
	if code != http.StatusConflict || out["code"] != "restore_point_failed" {
		t.Fatalf("restore went ahead without a restore point: %d %v", code, out)
	}
	if got, _ := h.ftp.Get("/etc/enigma2/bouquets.tv"); string(got) != sampleIndex {
		t.Fatalf("bouquets.tv was overwritten: %q", got)
	}
	if _, ok := h.ftp.Get("/etc/enigma2/lamedb"); ok {
		t.Fatal("lamedb was written")
	}
}

// The same restore works when a restore point can be saved, so the test above is not vacuous.
func TestRestoreRunsWhenRestorePointIsSaved(t *testing.T) {
	h := newHarness(t)
	zipData := makeZip(t, map[string]string{"bouquets.tv": "#NAME Other\n", "lamedb": "eDVB services /4/\n"})
	code, out := h.postRestore(zipData)
	if code != http.StatusOK {
		t.Fatalf("restore failed: %d %v", code, out)
	}
	if got, _ := h.ftp.Get("/etc/enigma2/bouquets.tv"); string(got) != "#NAME Other\n" {
		t.Fatalf("bouquets.tv not restored: %q", got)
	}
	matches, _ := filepath.Glob(filepath.Join(h.dir, "restore-points", "*", "*-before-restore.zip"))
	if len(matches) != 1 {
		t.Fatalf("expected one before-restore point, found %v", matches)
	}
}
