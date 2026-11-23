package httpapi

import (
	"net/url"
	"strings"

	"github.com/didou07/dcc-lite/src/filetransfer"
	"testing"
)

func (h *harness) readFile(path string) (file map[string]any, code int) {
	var out struct {
		File map[string]any `json:"file"`
	}
	code = h.do("GET", h.url("/content?path="+url.QueryEscape(path)), nil, &out)
	return out.File, code
}

func (h *harness) saveFile(m map[string]any) (out map[string]any, code int) {
	code = h.do("PUT", h.url("/content"), m, &out)
	return out, code
}

// openAndSave opens a file as the editor does and saves it back unchanged.
func (h *harness) openAndSave(t *testing.T, path string) map[string]any {
	t.Helper()
	f, code := h.readFile(path)
	if code != 200 {
		t.Fatalf("open %s: %d", path, code)
	}
	out, code := h.saveFile(map[string]any{
		"path": path, "content": f["content"], "line_ending": f["line_ending"], "encoding": f["encoding"],
		"expected_size": f["size"], "expected_modified_at": f["modified_at"], "expected_hash": f["hash"],
	})
	if code != 200 {
		t.Fatalf("save %s: %d %v", path, code, out)
	}
	return out
}

func TestEditorOpenAndSaveKeepsEveryByte(t *testing.T) {
	h := newHarness(t)
	const dir = "/etc/enigma2/"
	files := map[string]string{
		"crlf.txt":         "a\r\nb\r\n",
		"bom.txt":          "\xef\xbb\xbfhello\n",
		"empty.txt":        "",
		"no newline.txt":   "abc",
		"arabic.txt":       "مرحبا\n",
		"lone cr.txt":      "a\rb\r",
		"latin1.txt":       "caf\xe9 = 1\n",
		"codepage1256.txt": "name=\xe3\xd1\xcd\xc8\xc7\n",
		"latin1 crlf.txt":  "caf\xe9\r\nx\r\n",
	}
	for name, content := range files {
		h.ftp.Put(dir+name, []byte(content))
		h.openAndSave(t, dir+name)
		if got, _ := h.ftp.Get(dir + name); string(got) != content {
			t.Errorf("%s: saved %q, want %q", name, got, content)
		}
	}
}

func TestEditorOpensNonUTF8FilesAsLatin1(t *testing.T) {
	h := newHarness(t)
	h.ftp.Put("/etc/enigma2/old.cfg", []byte("caf\xe9\n"))
	f, code := h.readFile("/etc/enigma2/old.cfg")
	if code != 200 || f["encoding"] != "latin1" || f["content"] != "café\n" {
		t.Fatalf("%d %v", code, f)
	}
	// Edit the ASCII part; the unknown bytes must survive untouched.
	_, code = h.saveFile(map[string]any{
		"path": "/etc/enigma2/old.cfg", "content": "café = 2\n", "line_ending": "lf", "encoding": "latin1",
		"expected_size": f["size"], "expected_modified_at": f["modified_at"], "expected_hash": f["hash"],
	})
	if got, _ := h.ftp.Get("/etc/enigma2/old.cfg"); code != 200 || string(got) != "caf\xe9 = 2\n" {
		t.Fatalf("%d %q", code, got)
	}
	// Typing characters Latin-1 cannot hold is refused clearly and writes nothing.
	f, _ = h.readFile("/etc/enigma2/old.cfg")
	out, code := h.saveFile(map[string]any{
		"path": "/etc/enigma2/old.cfg", "content": "مرحبا\n", "line_ending": "lf", "encoding": "latin1",
		"expected_size": f["size"], "expected_modified_at": f["modified_at"], "expected_hash": f["hash"],
	})
	if code != 422 || out["code"] != "encoding" {
		t.Fatalf("%d %v", code, out)
	}
	if got, _ := h.ftp.Get("/etc/enigma2/old.cfg"); string(got) != "caf\xe9 = 2\n" {
		t.Fatalf("a refused save changed the file: %q", got)
	}
}

func TestEditorStillRefusesBinaryFiles(t *testing.T) {
	h := newHarness(t)
	h.ftp.Put("/etc/enigma2/data.bin", []byte("ELF\x00\x01\x02\xff"))
	if _, code := h.readFile("/etc/enigma2/data.bin"); code != 422 {
		t.Fatalf("binary file opened: %d", code)
	}
}

func TestEditorMixedLineEndingsUseTheMostCommonOne(t *testing.T) {
	h := newHarness(t)
	h.ftp.Put("/etc/enigma2/mixed.txt", []byte("a\r\nb\nc\r\n"))
	f, _ := h.readFile("/etc/enigma2/mixed.txt")
	if f["line_ending"] != "crlf" || f["mixed_line_endings"] != true {
		t.Fatalf("line_ending = %v, mixed = %v", f["line_ending"], f["mixed_line_endings"])
	}
	h.openAndSave(t, "/etc/enigma2/mixed.txt")
	if got, _ := h.ftp.Get("/etc/enigma2/mixed.txt"); string(got) != "a\r\nb\r\nc\r\n" {
		t.Fatalf("saved %q", got)
	}
}

func TestEditorLeavesNoTemporaryFilesBehind(t *testing.T) {
	h := newHarness(t)
	h.ftp.Put("/etc/enigma2/x.txt", []byte("1\n"))
	h.openAndSave(t, "/etc/enigma2/x.txt")
	for _, name := range h.ftp.Files() {
		if strings.Contains(name, "dcc") && strings.Contains(name, "edit") || strings.Contains(name, "backup-") || strings.HasSuffix(name, ".part") {
			t.Errorf("leftover temporary file %s", name)
		}
	}
}

func TestEditorCreateErrors(t *testing.T) {
	h := newHarness(t)
	out, code := h.saveFile(map[string]any{"path": "/etc/enigma2/nodir/new.txt", "content": "x", "create": true})
	if code != 404 || out["code"] != "path_not_found" {
		t.Fatalf("missing folder: %d %v", code, out)
	}
	if _, code = h.saveFile(map[string]any{"path": "/etc/enigma2/new.txt", "content": "x\n", "create": true}); code != 200 {
		t.Fatalf("create: %d", code)
	}
	if out, code = h.saveFile(map[string]any{"path": "/etc/enigma2/new.txt", "content": "y\n", "create": true}); code != 409 || out["code"] != "exists" {
		t.Fatalf("create twice: %d %v", code, out)
	}
	if d, _ := h.ftp.Get("/etc/enigma2/new.txt"); string(d) != "x\n" {
		t.Fatalf("second create overwrote the file: %q", d)
	}
	if _, code = h.saveFile(map[string]any{"path": "/etc/enigma2/big.txt", "content": strings.Repeat("a", filetransfer.MaxEditBytes+1), "create": true}); code != 413 {
		t.Fatalf("too large: %d", code)
	}
}

// A channel list (lamedb) on a receiver with many channels is well over 1 MB; it must open and save.
func TestEditorOpensAndSavesAFileOverOneMegabyte(t *testing.T) {
	h := newHarness(t)
	line := "0001:00000001:0001:0001:1:0\nChannel name\np:Provider\n"
	big := strings.Repeat(line, 3<<20/len(line))
	h.ftp.Put("/etc/enigma2/lamedb", []byte(big))
	f, _ := h.readFile("/etc/enigma2/lamedb")
	if f["content"] == nil || int(f["size"].(float64)) != len(big) {
		t.Fatalf("a %d byte file did not open: %v", len(big), f["error"])
	}
	h.openAndSave(t, "/etc/enigma2/lamedb")
	if got, _ := h.ftp.Get("/etc/enigma2/lamedb"); string(got) != big {
		t.Fatalf("a save changed the file: %d bytes now, was %d", len(got), len(big))
	}
}
