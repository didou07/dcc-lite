package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/filetransfer"
	"github.com/didou07/dcc-lite/src/filetransfer/ftptest"
)

const (
	sampleIndex = "#NAME User - Bouquets (TV)\n#SERVICE 1:7:1:0:0:0:0:0:0:0:FROM BOUQUET \"userbouquet.favourites.tv\" ORDER BY bouquet\n"
	sampleTV    = "#NAME Favourites\n#SERVICE 1:0:1:6DCA:44D:1:C00000:0:0:0:\n#DESCRIPTION Channel One\n"
)

// harness runs the real HTTP handlers against a fake FTP receiver.
type harness struct {
	t       *testing.T
	ftp     *ftptest.Server
	srv     *httptest.Server
	session string
	dir     string // the app's data directory
	app     *app.App
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	fs := ftptest.Start(t)
	fs.MkDir("/etc")
	fs.MkDir("/etc/enigma2")
	fs.Put("/etc/enigma2/bouquets.tv", []byte(sampleIndex))
	fs.Put("/etc/enigma2/userbouquet.favourites.tv", []byte(sampleTV))

	dir := t.TempDir()
	store, err := config.NewProfileStore(dir, dir+"/settings.ini")
	if err != nil {
		t.Fatal(err)
	}
	prof, err := store.Save(config.Profile{Name: "box", Host: "127.0.0.1", Username: "root", Password: "secret", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	application := app.New("test", store, dir)
	t.Cleanup(application.Close)
	srv := httptest.NewServer(New(application))
	t.Cleanup(srv.Close)

	h := &harness{t: t, ftp: fs, srv: srv, dir: dir, app: application}
	var out struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	code := h.do("POST", "/api/v1/files/sessions", map[string]any{"profile_id": prof.ID, "protocol": "ftp", "port": fs.Port()}, &out)
	if code != http.StatusCreated || out.Session.ID == "" {
		t.Fatalf("open session: %d %+v", code, out)
	}
	h.session = out.Session.ID
	return h
}

func (h *harness) do(method, path string, body any, out any) int {
	h.t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, h.srv.URL+path, rd)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil && len(raw) > 0 {
		if err := json.Unmarshal(raw, out); err != nil {
			h.t.Fatalf("%s %s: bad JSON %q: %v", method, path, raw, err)
		}
	}
	return resp.StatusCode
}

func (h *harness) url(suffix string) string { return "/api/v1/files/sessions/" + h.session + suffix }

func TestBouquetListAndRead(t *testing.T) {
	h := newHarness(t)
	var list struct {
		TV []struct{ File, Name, Kind string } `json:"tv"`
	}
	if code := h.do("GET", h.url("/enigma2/bouquets"), nil, &list); code != 200 || len(list.TV) != 1 || list.TV[0].Name != "Favourites" || list.TV[0].File != "userbouquet.favourites.tv" {
		t.Fatalf("%d %+v", code, list)
	}
	var got struct {
		Name    string `json:"name"`
		Hash    string `json:"hash"`
		Entries []struct {
			Ref         string `json:"ref"`
			Description string `json:"description"`
		} `json:"entries"`
	}
	if code := h.do("GET", h.url("/enigma2/bouquet?file=userbouquet.favourites.tv"), nil, &got); code != 200 || got.Name != "Favourites" || len(got.Entries) != 1 || got.Hash == "" {
		t.Fatalf("%d %+v", code, got)
	}
	var e map[string]any
	if code := h.do("GET", h.url("/enigma2/bouquet?file=../passwd"), nil, &e); code != 400 {
		t.Fatalf("bad file name accepted: %d", code)
	}
	if code := h.do("GET", h.url("/enigma2/bouquet?file=userbouquet.missing.tv"), nil, &e); code != 404 || e["code"] != "file_not_found" {
		t.Fatalf("missing: %d %v", code, e)
	}
}

func TestBouquetListMissingFolder(t *testing.T) {
	h := newHarness(t)
	var e map[string]any
	if code := h.do("GET", h.url("/enigma2/bouquets?dir=/nowhere"), nil, &e); code == 200 {
		t.Fatalf("no error for a folder without bouquets: %v", e)
	}
	if code := h.do("GET", h.url("/enigma2/bouquets?dir=relative"), nil, &e); code != 400 {
		t.Fatalf("relative dir: %d", code)
	}
}

func TestBouquetSaveWritesFileAndKeepsRestorePoint(t *testing.T) {
	h := newHarness(t)
	var cur struct {
		Hash string `json:"hash"`
	}
	h.do("GET", h.url("/enigma2/bouquet?file=userbouquet.favourites.tv"), nil, &cur)

	save := map[string]any{
		"file": "userbouquet.favourites.tv", "name": "Renamed", "expected_hash": cur.Hash,
		"entries": []map[string]string{
			{"ref": "1:0:1:6DCA:44D:1:C00000:0:0:0:", "description": "Channel One"},
			{"ref": "1:0:19:283D:3FB:1:C00000:0:0:0:", "description": "Channel Two"},
		},
	}
	var out struct {
		Count int    `json:"count"`
		Hash  string `json:"hash"`
	}
	if code := h.do("PUT", h.url("/enigma2/bouquet"), save, &out); code != 200 || out.Count != 2 || out.Hash == "" || out.Hash == cur.Hash {
		t.Fatalf("save: %d %+v", code, out)
	}
	data, _ := h.ftp.Get("/etc/enigma2/userbouquet.favourites.tv")
	text := string(data)
	if !strings.HasPrefix(text, "#NAME Renamed\n") || !strings.Contains(text, "Channel Two") || strings.Contains(text, "\r") {
		t.Fatalf("file on the receiver = %q", text)
	}

	// The same expected_hash is now stale: saving again must be refused, not overwrite.
	var e map[string]any
	if code := h.do("PUT", h.url("/enigma2/bouquet"), save, &e); code != http.StatusConflict || e["code"] != "conflict" {
		t.Fatalf("stale save: %d %v", code, e)
	}
	// Force overrides the conflict check.
	save["force"] = true
	if code := h.do("PUT", h.url("/enigma2/bouquet"), save, &e); code != 200 {
		t.Fatalf("forced save: %d %v", code, e)
	}
}

func TestBouquetSaveValidation(t *testing.T) {
	h := newHarness(t)
	base := func() map[string]any {
		return map[string]any{"file": "userbouquet.favourites.tv", "name": "N", "entries": []map[string]string{}}
	}
	cases := map[string]func(m map[string]any){
		"bad file":     func(m map[string]any) { m["file"] = "../x" },
		"empty name":   func(m map[string]any) { m["name"] = "  " },
		"newline name": func(m map[string]any) { m["name"] = "a\nb" },
		"long name":    func(m map[string]any) { m["name"] = strings.Repeat("a", 121) },
		"bad ref":      func(m map[string]any) { m["entries"] = []map[string]string{{"ref": "not a ref\n#SERVICE evil"}} },
		"bad dir":      func(m map[string]any) { m["dir"] = "etc" },
	}
	for name, mutate := range cases {
		m := base()
		mutate(m)
		var e map[string]any
		if code := h.do("PUT", h.url("/enigma2/bouquet"), m, &e); code != http.StatusBadRequest {
			t.Errorf("%s: status %d (%v)", name, code, e)
		}
	}
	if code := h.do("PUT", h.url("/enigma2/bouquet"), map[string]any{"unknown": 1}, nil); code != http.StatusBadRequest {
		t.Errorf("unknown field: %d", code)
	}
	data, _ := h.ftp.Get("/etc/enigma2/userbouquet.favourites.tv")
	if string(data) != sampleTV {
		t.Fatalf("a rejected save changed the file: %q", data)
	}
}

func TestRemoteFileEditConflictAndRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.ftp.Put("/etc/enigma2/notes.txt", []byte("one\ntwo\n"))
	var rd struct {
		File struct {
			Content string `json:"content"`
			Hash    string `json:"hash"`
			Size    int64  `json:"size"`
		} `json:"file"`
	}
	if code := h.do("GET", h.url("/content?path=/etc/enigma2/notes.txt"), nil, &rd); code != 200 || rd.File.Content != "one\ntwo\n" {
		t.Fatalf("%d %+v", code, rd)
	}
	var e map[string]any
	ok := map[string]any{"path": "/etc/enigma2/notes.txt", "content": "one\nTWO\n", "line_ending": "lf", "expected_hash": rd.File.Hash, "expected_size": rd.File.Size}
	if code := h.do("PUT", h.url("/content"), ok, &e); code != 200 {
		t.Fatalf("save: %d %v", code, e)
	}
	if d, _ := h.ftp.Get("/etc/enigma2/notes.txt"); string(d) != "one\nTWO\n" {
		t.Fatalf("receiver has %q", d)
	}
	h.ftp.Put("/etc/enigma2/notes.txt", []byte("changed elsewhere\n"))
	if code := h.do("PUT", h.url("/content"), ok, &e); code != http.StatusConflict {
		t.Fatalf("overwrote a file changed on the receiver: %d %v", code, e)
	}
	if d, _ := h.ftp.Get("/etc/enigma2/notes.txt"); string(d) != "changed elsewhere\n" {
		t.Fatalf("receiver has %q", d)
	}
	if code := h.do("GET", h.url("/content?path=/etc/enigma2/none.txt"), nil, &e); code != 404 {
		t.Fatalf("missing file: %d", code)
	}
	if code := h.do("GET", h.url("/content"), nil, &e); code != 400 {
		t.Fatalf("no path: %d", code)
	}
}

func TestRemoteFileOperationsOverHTTP(t *testing.T) {
	h := newHarness(t)
	h.ftp.Put("/etc/enigma2/a.txt", []byte("A"))
	var list struct {
		Path    string `json:"path"`
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if code := h.do("GET", h.url("/list?path=/etc/enigma2"), nil, &list); code != 200 || len(list.Entries) < 3 {
		t.Fatalf("%d %+v", code, list)
	}
	var e map[string]any
	if code := h.do("POST", h.url("/mkdir"), map[string]any{"path": "/etc/enigma2/new"}, &e); code != 200 && code != 201 && code != 204 {
		t.Fatalf("mkdir: %d %v", code, e)
	}
	if code := h.do("POST", h.url("/rename"), map[string]any{"old_path": "/etc/enigma2/a.txt", "new_path": "/etc/enigma2/b.txt"}, &e); code != http.StatusNoContent {
		t.Fatalf("rename: %d %v", code, e)
	}
	if _, ok := h.ftp.Get("/etc/enigma2/b.txt"); !ok {
		t.Fatal("rename did not reach the receiver")
	}
	if _, still := h.ftp.Get("/etc/enigma2/a.txt"); still {
		t.Fatal("old name still exists after rename")
	}
	if code := h.do("GET", "/api/v1/files/sessions/nosuchsession/list?path=/", nil, &e); code == 200 {
		t.Fatal("list on an unknown session succeeded")
	}
	if code := h.do("DELETE", h.url(""), nil, nil); code != http.StatusNoContent {
		t.Fatalf("close: %d", code)
	}
	if code := h.do("DELETE", h.url(""), nil, nil); code != http.StatusNotFound {
		t.Fatalf("second close: %d", code)
	}
}

func TestOpenSessionRejectsUnknownProfileAndBadBody(t *testing.T) {
	h := newHarness(t)
	var e map[string]any
	if code := h.do("POST", "/api/v1/files/sessions", map[string]any{"profile_id": "doesnotexist", "protocol": "ftp"}, &e); code != http.StatusNotFound {
		t.Fatalf("unknown profile: %d %v", code, e)
	}
	if code := h.do("POST", "/api/v1/files/sessions", map[string]any{"profile_id": "x", "bogus": 1}, &e); code != http.StatusBadRequest {
		t.Fatalf("unknown field: %d", code)
	}
}

func TestBackupThenRestoreRoundTrip(t *testing.T) {
	h := newHarness(t)
	h.ftp.Put("/etc/enigma2/lamedb", []byte("eDVB services /4/\n"))

	resp, err := http.Get(h.srv.URL + h.url("/enigma2/backup"))
	if err != nil {
		t.Fatal(err)
	}
	zipData, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || len(zipData) < 100 {
		t.Fatalf("backup: %d, %d bytes", resp.StatusCode, len(zipData))
	}
	files, _, err := readRestoreZip(zipData)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, f := range files {
		got[f.Name] = true
	}
	for _, want := range []string{"lamedb", "bouquets.tv", "userbouquet.favourites.tv"} {
		if !got[want] {
			t.Errorf("backup is missing %s (has %v)", want, got)
		}
	}

	// The user breaks the bouquet, then restores the backup.
	h.ftp.Put("/etc/enigma2/userbouquet.favourites.tv", []byte("#NAME Broken\n"))
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "backup.zip")
	fw.Write(zipData)
	mw.Close()
	req, _ := http.NewRequest("POST", h.srv.URL+h.url("/enigma2/restore"), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rresp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(rresp.Body)
	rresp.Body.Close()
	if rresp.StatusCode != 200 {
		t.Fatalf("restore: %d %s", rresp.StatusCode, raw)
	}
	if d, _ := h.ftp.Get("/etc/enigma2/userbouquet.favourites.tv"); string(d) != sampleTV {
		t.Fatalf("bouquet after restore = %q", d)
	}

	// A restore point of the broken state was taken first, so the restore can be undone.
	var points map[string]any
	if code := h.do("GET", h.url("/enigma2/restore-points"), nil, &points); code != 200 {
		t.Fatalf("restore points: %d", code)
	}
	if list, _ := points["points"].([]any); len(list) == 0 {
		t.Fatalf("no restore point was kept before restoring: %v", points)
	}
}

func TestRestoreRejectsNonMultipartAndGarbage(t *testing.T) {
	h := newHarness(t)
	var e map[string]any
	if code := h.do("POST", h.url("/enigma2/restore"), map[string]any{"x": 1}, &e); code != http.StatusBadRequest {
		t.Fatalf("json body: %d %v", code, e)
	}
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "backup.zip")
	fw.Write([]byte("this is not a zip"))
	mw.Close()
	req, _ := http.NewRequest("POST", h.srv.URL+h.url("/enigma2/restore"), &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("garbage upload: %d", resp.StatusCode)
	}
	if d, _ := h.ftp.Get("/etc/enigma2/userbouquet.favourites.tv"); string(d) != sampleTV {
		t.Fatal("a rejected restore changed the receiver")
	}
}

func (h *harness) files() *filetransfer.Manager { return h.app.Files }
