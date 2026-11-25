package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func (h *harness) getBytes(path string) (int, []byte, http.Header) {
	h.t.Helper()
	resp, err := http.Get(h.srv.URL + h.url("/bytes?path="+url.QueryEscape(path)))
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, data, resp.Header
}

func TestBytesEndpointReturnsBinaryFilesExactly(t *testing.T) {
	h := newHarness(t)
	binary := []byte{0x00, 0x01, 0xfe, 0xff, 'e', 'p', 'g', 0x00, 0x7f, 0x80}
	h.ftp.Put("/etc/enigma2/epg.dat", binary)
	h.ftp.Put("/etc/enigma2/empty.bin", nil)
	if _, code := h.readFile("/etc/enigma2/epg.dat"); code != http.StatusUnprocessableEntity {
		t.Fatalf("the text editor should still refuse binary data: %d", code)
	}
	code, data, header := h.getBytes("/etc/enigma2/epg.dat")
	if code != http.StatusOK || !bytes.Equal(data, binary) || header.Get("X-File-Size") != "10" || header.Get("X-File-Truncated") != "false" {
		t.Fatalf("bytes: %d % x %v", code, data, header)
	}
	if code, data, _ := h.getBytes("/etc/enigma2/empty.bin"); code != http.StatusOK || len(data) != 0 {
		t.Fatalf("empty file: %d %d", code, len(data))
	}
	if code, _, _ := h.getBytes("/etc/enigma2/missing.bin"); code != http.StatusNotFound {
		t.Fatalf("missing file: %d", code)
	}
	if code, _, _ := h.getBytes("/etc/enigma2"); code == http.StatusOK {
		t.Fatal("a folder was returned as bytes")
	}
}

func TestReadHeadCutsOffBigFilesAndKeepsTheSession(t *testing.T) {
	h := newHarness(t)
	big := make([]byte, 3<<20)
	for i := range big {
		big[i] = byte(i * 7)
	}
	h.ftp.Put("/etc/enigma2/big.bin", big)
	h.ftp.Put("/etc/enigma2/small.txt", []byte("still works\n"))
	ctx := t.Context()
	file, found, err := h.files().ReadHead(ctx, h.session, "/etc/enigma2/big.bin", 200<<10)
	head := file.Data
	if err != nil || !found || file.Size != int64(len(big)) || !file.Truncated || file.Hash != "" {
		t.Fatalf("head: %v found=%v size=%d truncated=%v hash=%q", err, found, file.Size, file.Truncated, file.Hash)
	}
	if len(head) != 200<<10 || !bytes.Equal(head, big[:len(head)]) {
		t.Fatalf("the first %d bytes are wrong (got %d)", 200<<10, len(head))
	}
	if f, code := h.readFile("/etc/enigma2/small.txt"); code != http.StatusOK || f["content"] != "still works\n" {
		t.Fatalf("the session broke after a cut-off download: %d %v", code, f)
	}
	again, _, err := h.files().ReadHead(ctx, h.session, "/etc/enigma2/big.bin", 200<<10)
	if err != nil || !bytes.Equal(again.Data, head) {
		t.Fatalf("second read: %v", err)
	}
}

func TestLocalBytes(t *testing.T) {
	h := newHarness(t)
	file := filepath.Join(t.TempDir(), "x.bin")
	if err := os.WriteFile(file, []byte{0, 1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}
	resp, err := http.Get(h.srv.URL + "/api/v1/files/local/bytes?path=" + url.QueryEscape(file))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || !bytes.Equal(data, []byte{0, 1, 2, 3}) {
		t.Fatalf("%d % x", resp.StatusCode, data)
	}
}

func (h *harness) putBytes(path string, data []byte, header http.Header, query string) (int, map[string]any) {
	h.t.Helper()
	req, _ := http.NewRequest("PUT", h.srv.URL+h.url("/bytes?path="+url.QueryEscape(path)+query), bytes.NewReader(data))
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

func (h *harness) opened(path string) string {
	h.t.Helper()
	_, _, header := h.getBytes(path)
	return "&size=" + header.Get("X-File-Size") + "&modified=" + url.QueryEscape(header.Get("X-File-Modified")) + "&hash=" + header.Get("X-File-Hash")
}

func TestHexEditSavesAndKeepsACopyOfTheOriginal(t *testing.T) {
	h := newHarness(t)
	original := []byte{0x00, 0x01, 0x02, 0x03, 0xff}
	h.ftp.Put("/etc/enigma2/epg.dat", original)
	query := h.opened("/etc/enigma2/epg.dat")

	edited := []byte{0x00, 0xAA, 0x02, 0x03, 0xff, 0x00} // one byte changed, one added
	code, out := h.putBytes("/etc/enigma2/epg.dat", edited, nil, query)
	if code != http.StatusOK {
		t.Fatalf("save: %d %v", code, out)
	}
	if got, _ := h.ftp.Get("/etc/enigma2/epg.dat"); !bytes.Equal(got, edited) {
		t.Fatalf("receiver has % x", got)
	}
	kept, _ := out["original_kept"].(string)
	if copyData, err := os.ReadFile(kept); err != nil || !bytes.Equal(copyData, original) {
		t.Fatalf("the original was not kept (%q): %v % x", kept, err, copyData)
	}
	if out["file"].(map[string]any)["size"].(float64) != 6 {
		t.Fatalf("response: %v", out)
	}
	for _, f := range h.ftp.Files() {
		if strings.Contains(f, "partial") || strings.Contains(f, ".dcc-edit") {
			t.Fatalf("a temporary file was left on the receiver: %s", f)
		}
	}
}

func TestHexEditRefusesWhenTheFileChangedMeanwhile(t *testing.T) {
	h := newHarness(t)
	h.ftp.Put("/etc/enigma2/a.bin", []byte{1, 2, 3})
	query := h.opened("/etc/enigma2/a.bin")
	h.ftp.Put("/etc/enigma2/a.bin", []byte{9, 9, 9}) // someone else, same size
	code, out := h.putBytes("/etc/enigma2/a.bin", []byte{1, 2, 4}, nil, query)
	if code != http.StatusConflict || out["code"] != "conflict" {
		t.Fatalf("save over a changed file: %d %v", code, out)
	}
	if got, _ := h.ftp.Get("/etc/enigma2/a.bin"); !bytes.Equal(got, []byte{9, 9, 9}) {
		t.Fatalf("the other change was overwritten: % x", got)
	}
	// The person can still choose to overwrite.
	if code, out := h.putBytes("/etc/enigma2/a.bin", []byte{1, 2, 4}, nil, query+"&force=1"); code != http.StatusOK {
		t.Fatalf("forced save: %d %v", code, out)
	}
}

func TestHexEditWritesNothingWhenTheCopyCannotBeKept(t *testing.T) {
	h := newHarness(t)
	h.ftp.Put("/etc/enigma2/a.bin", []byte{1, 2, 3})
	query := h.opened("/etc/enigma2/a.bin")
	if err := os.WriteFile(filepath.Join(h.dir, "file-backups"), []byte("in the way"), 0o600); err != nil {
		t.Fatal(err)
	}
	code, out := h.putBytes("/etc/enigma2/a.bin", []byte{7, 7, 7}, nil, query)
	if code == http.StatusOK {
		t.Fatalf("saved without a copy of the original: %v", out)
	}
	if got, _ := h.ftp.Get("/etc/enigma2/a.bin"); !bytes.Equal(got, []byte{1, 2, 3}) {
		t.Fatalf("the file was changed: % x", got)
	}
}

func TestHexEditNeedsTheOpenedVersionAndRefusesBigFiles(t *testing.T) {
	h := newHarness(t)
	h.ftp.Put("/etc/enigma2/a.bin", []byte{1})
	if code, _ := h.putBytes("/etc/enigma2/a.bin", []byte{2}, nil, ""); code != http.StatusBadRequest {
		t.Fatalf("a save without the opened version: %d", code)
	}
	big := make([]byte, 16<<20+10)
	h.ftp.Put("/etc/enigma2/big.bin", big)
	if code, out := h.putBytes("/etc/enigma2/big.bin", []byte{1}, nil, "&force=1"); code != http.StatusRequestEntityTooLarge {
		t.Fatalf("saved a cut-off copy over a big file: %d %v", code, out)
	}
	if got, _ := h.ftp.Get("/etc/enigma2/big.bin"); len(got) != len(big) {
		t.Fatal("the big file was replaced")
	}
}

func TestLocalHexEdit(t *testing.T) {
	h := newHarness(t)
	file := filepath.Join(t.TempDir(), "x.bin")
	if err := os.WriteFile(file, []byte{0, 1, 2, 3}, 0o640); err != nil {
		t.Fatal(err)
	}
	get, err := http.Get(h.srv.URL + "/api/v1/files/local/bytes?path=" + url.QueryEscape(file))
	if err != nil {
		t.Fatal(err)
	}
	get.Body.Close()
	query := "&size=" + get.Header.Get("X-File-Size") + "&modified=" + url.QueryEscape(get.Header.Get("X-File-Modified")) + "&hash=" + get.Header.Get("X-File-Hash")
	req, _ := http.NewRequest("PUT", h.srv.URL+"/api/v1/files/local/bytes?path="+url.QueryEscape(file)+query, bytes.NewReader([]byte{0, 9, 2, 3}))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("local save: %d", resp.StatusCode)
	}
	if got, _ := os.ReadFile(file); !bytes.Equal(got, []byte{0, 9, 2, 3}) {
		t.Fatalf("file has % x", got)
	}
}
