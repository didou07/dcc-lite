package httpapi

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/filetransfer"
)

const (
	tvIndex = "#NAME Bouquets (TV)\n" +
		"#SERVICE 1:7:1:0:0:0:0:0:0:0:FROM BOUQUET \"userbouquet.favourites.tv\" ORDER BY bouquet\n" +
		"#SERVICE 1:7:1:0:0:0:0:0:0:0:FROM BOUQUET \"userbouquet.news.tv\" ORDER BY bouquet\n"
	favourites = "#NAME Favourites (TV)\n#SORT 1\n" +
		"#SERVICE 1:0:19:2B66:3F3:1:C00000:0:0:0:\n" +
		"#SERVICE 1:64:1:0:0:0:0:0:0:0:\n#DESCRIPTION Sports\n" +
		"#SERVICE 1:0:1:6DCA:44D:1:C00000:0:0:0:\n"
	lamedb = "eDVB services /4/\ntransponders\nend\nservices\n" +
		"2b66:00c00000:03f3:0001:25:0\nDas Erste HD\np:ARD\n" +
		"6dca:00c00000:044d:0001:1:0\nZDF\np:ZDF\nend\n"
)

type bouquetEnv struct {
	h   http.Handler
	app *app.App
	fs  *filetransfer.MemoryFS
	url string
}

func newBouquetEnv(t *testing.T) *bouquetEnv {
	t.Helper()
	store, err := config.NewProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	application := app.New("test", store, t.TempDir())
	t.Cleanup(application.Terminals.CloseAll)
	t.Cleanup(application.Files.CloseAll)
	fs := filetransfer.NewMemoryFS()
	fs.Set("/etc/enigma2/bouquets.tv", tvIndex, 0o644)
	fs.Set("/etc/enigma2/userbouquet.favourites.tv", favourites, 0o644)
	fs.Set("/etc/enigma2/userbouquet.news.tv", "#NAME News\n", 0o644)
	fs.Set("/etc/enigma2/lamedb", lamedb, 0o644)
	profile, err := store.Save(config.Profile{Name: "Box", Host: "127.0.0.1", Username: "root", Password: "pw", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	application.Files.AttachAdapter(filetransfer.SessionInfo{ID: "sess0001", ProfileID: profile.ID, Protocol: "ftp", Host: "127.0.0.1", Port: 21}, fs)
	return &bouquetEnv{h: New(application), app: application, fs: fs, url: "/api/v1/files/sessions/sess0001/enigma2/"}
}

func (e *bouquetEnv) do(t *testing.T, method, endpoint string, body any) (int, map[string]any) {
	t.Helper()
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	res := request(t, e.h, method, e.url+endpoint, raw)
	var out map[string]any
	_ = json.Unmarshal(res.Body.Bytes(), &out)
	return res.Code, out
}

func TestBouquetListShowsNamesAndFilesFromIndex(t *testing.T) {
	e := newBouquetEnv(t)
	code, out := e.do(t, http.MethodGet, "bouquets", nil)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	tv := out["tv"].([]any)
	if len(tv) != 2 || tv[0].(map[string]any)["name"] != "Favourites (TV)" || tv[1].(map[string]any)["file"] != "userbouquet.news.tv" {
		t.Fatalf("tv list = %v", tv)
	}
	if radio := out["radio"].([]any); len(radio) != 0 {
		t.Fatalf("radio list should be empty: %v", radio)
	}
	e.fs.Set("/etc/enigma2/x", "", 0)
	badDir, _ := e.do(t, http.MethodGet, "bouquets?dir=/nowhere", nil)
	if badDir == http.StatusOK {
		t.Fatal("a folder without bouquets must not succeed")
	}
}

func TestGetBouquetResolvesChannelNames(t *testing.T) {
	e := newBouquetEnv(t)
	code, out := e.do(t, http.MethodGet, "bouquet?file=userbouquet.favourites.tv", nil)
	if code != http.StatusOK {
		t.Fatalf("status %d: %v", code, out)
	}
	entries := out["entries"].([]any)
	names := []string{}
	for _, raw := range entries {
		names = append(names, raw.(map[string]any)["name"].(string))
	}
	if strings.Join(names, "|") != "Das Erste HD|Sports|ZDF" {
		t.Fatalf("names = %v", names)
	}
	if !entries[1].(map[string]any)["marker"].(bool) || entries[0].(map[string]any)["marker"].(bool) {
		t.Fatal("marker flags wrong")
	}
	if out["names_available"] != true || out["hash"] == "" {
		t.Fatalf("response = %v", out)
	}
	for _, bad := range []string{"bouquet?file=../passwd", "bouquet?file=", "bouquet?file=x.txt", "bouquet?file=a/b.tv"} {
		if code, _ := e.do(t, http.MethodGet, bad, nil); code != http.StatusBadRequest {
			t.Errorf("%s = %d, want 400", bad, code)
		}
	}
	if code, _ := e.do(t, http.MethodGet, "bouquet?file=userbouquet.missing.tv", nil); code != http.StatusNotFound {
		t.Errorf("missing bouquet = %d, want 404", code)
	}
}

func TestSaveBouquetReordersKeepsHeaderAndDetectsConflicts(t *testing.T) {
	e := newBouquetEnv(t)
	_, got := e.do(t, http.MethodGet, "bouquet?file=userbouquet.favourites.tv", nil)
	hash := got["hash"].(string)
	body := map[string]any{
		"file": "userbouquet.favourites.tv", "name": "My Favourites", "expected_hash": hash,
		"entries": []map[string]any{
			{"ref": "1:0:1:6DCA:44D:1:C00000:0:0:0:"},
			{"ref": "1:64:1:0:0:0:0:0:0:0:", "description": "Sports"},
		},
	}
	code, out := e.do(t, http.MethodPut, "bouquet", body)
	if code != http.StatusOK || out["count"].(float64) != 2 {
		t.Fatalf("save = %d %v", code, out)
	}
	text, _ := e.fs.Get("/etc/enigma2/userbouquet.favourites.tv")
	want := "#NAME My Favourites\n#SORT 1\n#SERVICE 1:0:1:6DCA:44D:1:C00000:0:0:0:\n#SERVICE 1:64:1:0:0:0:0:0:0:0:\n#DESCRIPTION Sports\n"
	if text != want {
		t.Fatalf("file now:\n%q\nwant:\n%q", text, want)
	}
	for _, name := range e.fs.Names() {
		if strings.Contains(name, ".dccnext-") {
			t.Fatalf("temporary file left behind: %s", name)
		}
	}

	// The browser still holds the old hash: somebody else (or this save) changed the file since.
	if code, out := e.do(t, http.MethodPut, "bouquet", body); code != http.StatusConflict || out["code"] != "conflict" {
		t.Fatalf("stale save = %d %v, want 409 conflict", code, out)
	}
	body["force"] = true
	if code, _ := e.do(t, http.MethodPut, "bouquet", body); code != http.StatusOK {
		t.Fatalf("forced save = %d", code)
	}
}

func TestSaveBouquetRejectsBadInput(t *testing.T) {
	e := newBouquetEnv(t)
	good := map[string]any{"file": "userbouquet.favourites.tv", "name": "N", "entries": []map[string]any{{"ref": "1:0:1:1:1:1:1:0:0:0:"}}}
	mutate := func(key string, value any) map[string]any {
		m := map[string]any{}
		for k, v := range good {
			m[k] = v
		}
		m[key] = value
		return m
	}
	cases := map[string]map[string]any{
		"path traversal":  mutate("file", "../../etc/passwd.tv"),
		"empty name":      mutate("name", " "),
		"newline in name": mutate("name", "a\n#SERVICE evil"),
		"injected ref":    mutate("entries", []map[string]any{{"ref": "1:0:1:1:1:1:1\n#NAME x"}}),
		"junk ref":        mutate("entries", []map[string]any{{"ref": "hello"}}),
		"bad dir":         mutate("dir", "etc/enigma2"),
	}
	before, _ := e.fs.Get("/etc/enigma2/userbouquet.favourites.tv")
	for label, body := range cases {
		if code, out := e.do(t, http.MethodPut, "bouquet", body); code != http.StatusBadRequest {
			t.Errorf("%s: status %d %v, want 400", label, code, out)
		}
	}
	if after, _ := e.fs.Get("/etc/enigma2/userbouquet.favourites.tv"); after != before {
		t.Fatal("a rejected save modified the file")
	}
}

func TestNewBouquetCreatesFileAndIndexLine(t *testing.T) {
	e := newBouquetEnv(t)
	e.fs.Set("/etc/enigma2/bouquets.radio", "#NAME Bouquets (Radio)\n", 0o644)
	code, out := e.do(t, http.MethodPost, "bouquet/new", map[string]any{"kind": "radio", "name": "Arabic Radio"})
	if code != http.StatusCreated {
		t.Fatalf("new = %d %v", code, out)
	}
	file := out["file"].(string)
	if !strings.HasPrefix(file, "userbouquet.dcc") || !strings.HasSuffix(file, ".radio") {
		t.Fatalf("file = %q", file)
	}
	if text, ok := e.fs.Get("/etc/enigma2/" + file); !ok || text != "#NAME Arabic Radio\n" {
		t.Fatalf("new bouquet file = %q, %v", text, ok)
	}
	index, _ := e.fs.Get("/etc/enigma2/bouquets.radio")
	if !strings.Contains(index, `1:7:2:0:0:0:0:0:0:0:FROM BOUQUET "`+file+`" ORDER BY bouquet`) {
		t.Fatalf("index = %q", index)
	}
	if code, _ := e.do(t, http.MethodPost, "bouquet/new", map[string]any{"kind": "satellite", "name": "x"}); code != http.StatusBadRequest {
		t.Fatalf("bad kind = %d", code)
	}
	// A missing index: nothing must be created.
	e.fs.Set("/etc/enigma2/only/lamedb", "", 0)
	if code, _ := e.do(t, http.MethodPost, "bouquet/new", map[string]any{"dir": "/etc/enigma2/only", "kind": "tv", "name": "x"}); code == http.StatusCreated {
		t.Fatal("created a bouquet without an index")
	}
	for _, name := range e.fs.Names() {
		if strings.HasPrefix(name, "/etc/enigma2/only/userbouquet") {
			t.Fatalf("orphan bouquet file %s", name)
		}
	}
}

func TestBouquetListChangeRemovesAndMoves(t *testing.T) {
	e := newBouquetEnv(t)
	if code, out := e.do(t, http.MethodPost, "bouquet/list-change", map[string]any{"file": "userbouquet.news.tv", "action": "up"}); code != http.StatusOK || out["changed"] != true {
		t.Fatalf("up = %d %v", code, out)
	}
	index, _ := e.fs.Get("/etc/enigma2/bouquets.tv")
	if strings.Index(index, "news") > strings.Index(index, "favourites") {
		t.Fatalf("news should be first now:\n%s", index)
	}
	if _, out := e.do(t, http.MethodPost, "bouquet/list-change", map[string]any{"file": "userbouquet.news.tv", "action": "up"}); out["changed"] != false {
		t.Fatalf("moving the first entry up reported a change: %v", out)
	}
	if code, _ := e.do(t, http.MethodPost, "bouquet/list-change", map[string]any{"file": "userbouquet.news.tv", "action": "remove"}); code != http.StatusOK {
		t.Fatalf("remove = %d", code)
	}
	index, _ = e.fs.Get("/etc/enigma2/bouquets.tv")
	if strings.Contains(index, "news") {
		t.Fatalf("news still listed:\n%s", index)
	}
	if _, ok := e.fs.Get("/etc/enigma2/userbouquet.news.tv"); !ok {
		t.Fatal("removing a bouquet from the list must not delete its file")
	}
	if code, _ := e.do(t, http.MethodPost, "bouquet/list-change", map[string]any{"file": "userbouquet.news.tv", "action": "explode"}); code != http.StatusBadRequest {
		t.Fatalf("bad action = %d", code)
	}
}

func TestReloadCallsOnlyTheDevicesOwnWebInterface(t *testing.T) {
	e := newBouquetEnv(t)
	var gotPath, gotUser, gotPass string
	box := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.RequestURI()
		gotUser, gotPass, _ = r.BasicAuth()
		w.Write([]byte(`<e2simplexmlresult><e2state>True</e2state></e2simplexmlresult>`))
	}))
	defer box.Close()
	u, _ := url.Parse(box.URL)
	host, portText, _ := net.SplitHostPort(u.Host)
	port, _ := strconv.Atoi(portText)
	profiles := e.app.Profiles.List()
	profile, _ := e.app.Profiles.Get(profiles[0].ID)
	profile.Host, profile.Port = host, port
	if _, err := e.app.Profiles.Save(profile); err != nil {
		t.Fatal(err)
	}
	code, out := e.do(t, http.MethodPost, "reload", map[string]any{})
	if code != http.StatusOK || out["ok"] != true {
		t.Fatalf("reload = %d %v", code, out)
	}
	if gotPath != "/web/servicelistreload?mode=2" || gotUser != "root" || gotPass != "pw" {
		t.Fatalf("receiver saw %q user=%q pass=%q", gotPath, gotUser, gotPass)
	}

	box.Close()
	if code, out := e.do(t, http.MethodPost, "reload", map[string]any{}); code != http.StatusBadGateway || out["code"] != "web_unreachable" {
		t.Fatalf("unreachable web interface = %d %v", code, out)
	}
}
