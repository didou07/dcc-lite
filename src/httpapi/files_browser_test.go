package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// waitTask polls the transfer list until the task reaches a final state.
func (e *bouquetEnv) waitTask(t *testing.T, id string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		res := request(t, e.h, http.MethodGet, "/api/v1/files/transfers/"+id, nil)
		var out struct {
			Task map[string]any `json:"task"`
		}
		_ = json.Unmarshal(res.Body.Bytes(), &out)
		switch out.Task["state"] {
		case "completed", "failed", "cancelled":
			return out.Task
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("transfer did not finish in time")
	return nil
}

func TestUploadFromBrowserReachesTheReceiverAndCleansUp(t *testing.T) {
	e := newBouquetEnv(t)
	e.fs.Set("/tmp/.keep", "", 0o644) // the destination folder must exist on the receiver
	req := httptest.NewRequest(http.MethodPost,
		"http://127.0.0.1:8787/api/v1/files/sessions/sess0001/upload?path=/tmp/hello.txt&mtime=1700000000000",
		strings.NewReader("hello from the browser"))
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("upload status %d: %s", rec.Code, rec.Body)
	}
	var out struct {
		Task map[string]any `json:"task"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	task := e.waitTask(t, out.Task["id"].(string))
	if task["state"] != "completed" {
		t.Fatalf("task = %v", task)
	}
	if got, ok := e.fs.Get("/tmp/hello.txt"); !ok || got != "hello from the browser" {
		t.Fatalf("receiver file = %q, %v", got, ok)
	}
	// The staged copy must be gone once the receiver has the file.
	entries, _ := os.ReadDir(e.app.Staging.Root())
	if len(entries) != 0 {
		t.Fatalf("staging folder still holds %d item(s)", len(entries))
	}
}

func TestUploadFromBrowserRefusesBadRequests(t *testing.T) {
	e := newBouquetEnv(t)
	cases := []struct {
		name, target string
		want         int
	}{
		{"unknown session", "/api/v1/files/sessions/nope/upload?path=/tmp/a", http.StatusNotFound},
		{"relative path", "/api/v1/files/sessions/sess0001/upload?path=a.txt", http.StatusBadRequest},
		{"no path", "/api/v1/files/sessions/sess0001/upload", http.StatusBadRequest},
		{"root", "/api/v1/files/sessions/sess0001/upload?path=/", http.StatusBadRequest},
	}
	for _, c := range cases {
		res := request(t, e.h, http.MethodPost, c.target, []byte("x"))
		if res.Code != c.want {
			t.Errorf("%s: status %d, want %d (%s)", c.name, res.Code, c.want, res.Body)
		}
	}
	entries, _ := os.ReadDir(e.app.Staging.Root())
	if len(entries) != 0 {
		t.Fatalf("refused uploads left %d item(s) in staging", len(entries))
	}
}

func TestUploadFromBrowserDoesNotReplaceWithoutOverwrite(t *testing.T) {
	e := newBouquetEnv(t)
	e.fs.Set("/tmp/keep.txt", "original", 0o644)
	res := request(t, e.h, http.MethodPost, "/api/v1/files/sessions/sess0001/upload?path=/tmp/keep.txt", []byte("new"))
	var out struct {
		Task map[string]any `json:"task"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if res.Code != http.StatusAccepted {
		t.Fatalf("status %d: %s", res.Code, res.Body)
	}
	task := e.waitTask(t, out.Task["id"].(string))
	if task["state"] != "failed" {
		t.Fatalf("an existing file must not be replaced silently: %v", task)
	}
	if got, _ := e.fs.Get("/tmp/keep.txt"); got != "original" {
		t.Fatalf("file was changed to %q", got)
	}
	// With overwrite=1 it is replaced.
	res = request(t, e.h, http.MethodPost, "/api/v1/files/sessions/sess0001/upload?path=/tmp/keep.txt&overwrite=1", []byte("new"))
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	if task := e.waitTask(t, out.Task["id"].(string)); task["state"] != "completed" {
		t.Fatalf("overwrite upload: %v", task)
	}
	if got, _ := e.fs.Get("/tmp/keep.txt"); got != "new" {
		t.Fatalf("file = %q, want new", got)
	}
}

func TestDownloadToBrowserStreamsTheFileOnce(t *testing.T) {
	e := newBouquetEnv(t)
	e.fs.Set("/etc/hostname", "my receiver\n", 0o644)
	body, _ := json.Marshal(map[string]string{"remote_path": "/etc/hostname"})
	res := request(t, e.h, http.MethodPost, "/api/v1/files/sessions/sess0001/download-prepare", body)
	if res.Code != http.StatusAccepted {
		t.Fatalf("prepare status %d: %s", res.Code, res.Body)
	}
	var out struct {
		Task map[string]any `json:"task"`
	}
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	id := out.Task["id"].(string)
	if out.Task["staged"] != true {
		t.Fatalf("task is not marked staged: %v", out.Task)
	}
	if task := e.waitTask(t, id); task["state"] != "completed" {
		t.Fatalf("download task: %v", task)
	}
	got := request(t, e.h, http.MethodGet, "/api/v1/files/transfers/"+id+"/file", nil)
	if got.Code != http.StatusOK || !bytes.Equal(got.Body.Bytes(), []byte("my receiver\n")) {
		t.Fatalf("fetch status %d body %q", got.Code, got.Body)
	}
	if cd := got.Header().Get("Content-Disposition"); !strings.HasPrefix(cd, "attachment") || !strings.Contains(cd, "hostname") {
		t.Fatalf("Content-Disposition = %q", cd)
	}
	// The staged copy is released after a complete fetch.
	if again := request(t, e.h, http.MethodGet, "/api/v1/files/transfers/"+id+"/file", nil); again.Code == http.StatusOK {
		t.Fatal("the staged file must not be served twice")
	}
	entries, _ := os.ReadDir(e.app.Staging.Root())
	if len(entries) != 0 {
		t.Fatalf("staging folder still holds %d item(s)", len(entries))
	}
}

func TestFetchStagedDownloadRejectsOrdinaryTransfers(t *testing.T) {
	e := newBouquetEnv(t)
	if res := request(t, e.h, http.MethodGet, "/api/v1/files/transfers/missing/file", nil); res.Code != http.StatusNotFound {
		t.Fatalf("status %d, want 404", res.Code)
	}
}

func TestSafeStageName(t *testing.T) {
	for in, want := range map[string]string{
		"movie.mkv":         "movie.mkv",
		`C:\Users\me\a.ipk`: "a.ipk",
		"../../etc/passwd":  "passwd",
		"a:b*c?.txt":        "a_b_c_.txt",
		"..":                "file",
		"":                  "file",
		"  .hidden  ":       "hidden",
		"ملف.txt":           "ملف.txt",
	} {
		if got := safeStageName(in); got != want {
			t.Errorf("safeStageName(%q) = %q, want %q", in, got, want)
		}
	}
	if got := safeStageName(strings.Repeat("a", 400) + ".ipk"); len(got) > 150 {
		t.Errorf("name not shortened: %d bytes", len(got))
	}
}

func TestRemoteSessionCannotReachTheHostFiles(t *testing.T) {
	handler := remoteTestServer(t)
	cookie := signIn(t, handler)
	withCookie := func(req *http.Request) { sameOrigin(req); req.AddCookie(cookie) }
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "/api/v1/files/local/list?path=."},
		{http.MethodPost, "/api/v1/files/local/mkdir"},
		{http.MethodPost, "/api/v1/files/local/rename"},
		{http.MethodPost, "/api/v1/files/local/remove"},
		{http.MethodPost, "/api/v1/files/local/chmod"},
		{http.MethodPost, "/api/v1/files/sessions/any/transfer"},
	} {
		rec := remoteDo(handler, c.method, c.target, []byte(`{}`), withCookie)
		if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "host_files_unavailable") {
			t.Errorf("%s %s: status %d body %s, want 403 host_files_unavailable", c.method, c.target, rec.Code, rec.Body)
		}
	}
	// The same calls stay available on the local (loopback) listener.
	local := testServer(t)
	if res := request(t, local, http.MethodGet, "/api/v1/files/local/list?path=.", nil); res.Code != http.StatusOK {
		t.Fatalf("local listing status %d: %s", res.Code, res.Body)
	}
}

// The real server cuts a request off after ReadTimeout (15 s locally) and a reply
// after WriteTimeout (3 min). A big file takes longer than that; the upload and
// download handlers must lift both limits for their own request.
func TestBrowserUploadSurvivesTheServerReadTimeout(t *testing.T) {
	e := newBouquetEnv(t)
	e.fs.Set("/tmp/.keep", "", 0o644)
	server := httptest.NewUnstartedServer(e.h)
	server.Config.ReadTimeout = 300 * time.Millisecond
	server.Start()
	defer server.Close()

	reader, writer := io.Pipe()
	go func() {
		_, _ = writer.Write([]byte("first half, "))
		time.Sleep(900 * time.Millisecond) // three times the server's read timeout
		_, _ = writer.Write([]byte("second half"))
		_ = writer.Close()
	}()
	req, _ := http.NewRequest(http.MethodPost, server.URL+"/api/v1/files/sessions/sess0001/upload?path=/tmp/slow.txt", reader)
	req.Host = "127.0.0.1:8787"
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("slow upload failed: %v", err)
	}
	defer res.Body.Close()
	var out struct {
		Task map[string]any `json:"task"`
	}
	_ = json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != http.StatusAccepted {
		t.Fatalf("slow upload status %d", res.StatusCode)
	}
	if task := e.waitTask(t, out.Task["id"].(string)); task["state"] != "completed" {
		t.Fatalf("task = %v", task)
	}
	if got, _ := e.fs.Get("/tmp/slow.txt"); got != "first half, second half" {
		t.Fatalf("receiver got %q", got)
	}
}

func TestListServicesSearchesLamedb(t *testing.T) {
	e := newBouquetEnv(t)
	code, out := e.do(t, http.MethodGet, "services?kind=tv&q=zdf", nil)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	list := out["services"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["name"] != "ZDF" || list[0].(map[string]any)["ref"] != "1:0:1:6DCA:44D:1:C00000:0:0:0:" {
		t.Fatalf("services = %v", list)
	}
	_, all := e.do(t, http.MethodGet, "services?kind=tv", nil)
	if names := all["services"].([]any); len(names) != 2 || names[0].(map[string]any)["name"] != "Das Erste HD" {
		t.Fatalf("all = %v", names)
	}
	if code, _ := e.do(t, http.MethodGet, "services?kind=bad", nil); code != http.StatusBadRequest {
		t.Fatalf("bad kind status %d", code)
	}
	if _, radio := e.do(t, http.MethodGet, "services?kind=radio", nil); len(radio["services"].([]any)) != 0 {
		t.Fatalf("radio = %v", radio)
	}
}

func TestDeleteBouquetRemovesItFromTheListAndErasesTheFile(t *testing.T) {
	e := newBouquetEnv(t)
	code, out := e.do(t, http.MethodPost, "bouquet/list-change", map[string]string{"file": "userbouquet.news.tv", "action": "delete"})
	if code != http.StatusOK || out["file_removed"] != true {
		t.Fatalf("status %d: %v", code, out)
	}
	if _, ok := e.fs.Get("/etc/enigma2/userbouquet.news.tv"); ok {
		t.Fatal("the bouquet file must be gone")
	}
	if idx, _ := e.fs.Get("/etc/enigma2/bouquets.tv"); strings.Contains(idx, "news") {
		t.Fatalf("the index still lists it: %s", idx)
	}
}

func TestIPKInstallOptionsChangeTheCommand(t *testing.T) {
	e := newBouquetEnv(t)
	e.fs.Set("/tmp/.keep", "", 0o644)
	code, out := e.upload(t, "/api/v1/files/sessions/sess0001/ipk?force=1&restart=1", "enigma2-plugin-x_1.0~beta_all.ipk", []byte("ipk"))
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	want := "opkg install --force-reinstall --force-overwrite /tmp/enigma2-plugin-x_1.0~beta_all.ipk && rm -f /tmp/enigma2-plugin-x_1.0~beta_all.ipk && sleep 1 && init 4 && sleep 3 && init 3"
	if out["command"] != want {
		t.Fatalf("command = %v", out["command"])
	}
	_, plain := e.upload(t, "/api/v1/files/sessions/sess0001/ipk", "a.ipk", []byte("ipk"))
	if plain["command"] != "opkg install /tmp/a.ipk && rm -f /tmp/a.ipk" {
		t.Fatalf("default command = %v", plain["command"])
	}
}
