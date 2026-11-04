package httpapi

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
)

var tinyJPEG = []byte{0xFF, 0xD8, 0xFF, 0xE0, 0, 0x10, 'J', 'F', 'I', 'F', 0, 1, 1, 0, 0, 1, 0, 1, 0, 0, 0xFF, 0xD9}

// fakeReceiver stands in for OpenWebif and records what it was asked.
func fakeReceiver(t *testing.T, handler http.HandlerFunc) (host string, port int, requests *[]string) {
	t.Helper()
	var mu sync.Mutex
	var seen []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.URL.RequestURI())
		mu.Unlock()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	h, p, _ := net.SplitHostPort(srv.Listener.Addr().String())
	n, _ := strconv.Atoi(p)
	return h, n, &seen
}

func createDevice(t *testing.T, h http.Handler, host string, webPort int) string {
	t.Helper()
	body := `{"name":"Box","host":"` + host + `","web_port":` + strconv.Itoa(webPort) + `,"password":"pw"}`
	rec := request(t, h, http.MethodPost, "/api/v1/profiles", []byte(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Profile struct {
			ID   string `json:"id"`
			Port int    `json:"port"`
		} `json:"profile"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Profile.Port != webPort {
		t.Fatalf("web port was not saved: %+v", out.Profile)
	}
	return out.Profile.ID
}

func TestDeviceScreenProxiesAJPEGFromTheReceiver(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		if _, pass, ok := r.BasicAuth(); !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(tinyJPEG)
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)

	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/screen?mode=osd&width=1280", nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/jpeg" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("screen = %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Header().Get("Cache-Control"))
	}
	if string(rec.Body.Bytes()) != string(tinyJPEG) {
		t.Fatal("the picture was altered")
	}
	if len(*seen) != 1 || (*seen)[0] != "/grab?format=jpg&mode=osd&r=1280" {
		t.Fatalf("receiver was asked %v", *seen)
	}
	// Defaults.
	request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/screen", nil)
	if got := (*seen)[1]; got != "/grab?format=jpg&mode=all&r=720" {
		t.Fatalf("default request = %q", got)
	}
}

func TestDeviceScreenValidatesInputAndReportsDeviceProblems(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("r") {
		case "320":
			_, _ = w.Write([]byte("<html>not a picture</html>"))
		case "480":
			w.WriteHeader(http.StatusUnauthorized)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	get := func(q string) *httptest.ResponseRecorder {
		return request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/screen"+q, nil)
	}
	for _, q := range []string{"?mode=../../etc", "?mode=all%26x=1", "?width=99999", "?width=abc"} {
		if rec := get(q); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", q, rec.Code)
		}
	}
	if len(*seen) != 0 {
		t.Fatalf("invalid requests must never reach the receiver: %v", *seen)
	}
	if rec := get("?width=320"); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "did not return a picture") {
		t.Fatalf("non-JPEG = %d %s", rec.Code, rec.Body)
	}
	if rec := get("?width=480"); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "web_auth") {
		t.Fatalf("auth failure = %d %s", rec.Code, rec.Body)
	}
	if rec := get("?width=640"); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "HTTP 500") {
		t.Fatalf("device error = %d %s", rec.Code, rec.Body)
	}
	if rec := request(t, h, http.MethodGet, "/api/v1/profiles/nope/screen", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown device = %d", rec.Code)
	}
}

func TestDeviceScreenUnreachableWebInterface(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	h := testServer(t)
	id := createDevice(t, h, "127.0.0.1", port)
	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/screen", nil)
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "web_unreachable") || !strings.Contains(rec.Body.String(), strconv.Itoa(port)) {
		t.Fatalf("unreachable = %d %s", rec.Code, rec.Body)
	}
}

func TestSSHAndSFTPAreGone(t *testing.T) {
	h := testServer(t)
	for _, tc := range []struct{ method, path, body string }{
		{http.MethodPost, "/api/v1/profiles/x/ssh/host-key/scan", `{}`},
		{http.MethodPost, "/api/v1/profiles/x/ssh/host-key/trust", `{}`},
		{http.MethodGet, "/api/v1/internal/ssh-askpass/abc", ``},
	} {
		if rec := request(t, h, tc.method, tc.path, []byte(tc.body)); rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s should be gone, got %d", tc.path, rec.Code)
		}
	}
	for _, body := range []string{`{"profile_id":"x","protocol":"sftp"}`, `{"profile_id":"x","protocol":"ssh","columns":80,"rows":24}`} {
		path := "/api/v1/files/sessions"
		if strings.Contains(body, `"ssh"`) {
			path = "/api/v1/terminal/sessions"
		}
		if rec := request(t, h, http.MethodPost, path, []byte(body)); rec.Code < 400 {
			t.Fatalf("%s accepted %s", path, body)
		}
	}
}

func TestImportAcceptsExportsThatStillHaveSSHPort(t *testing.T) {
	h := testServer(t)
	body := `{"schema_version":2,"profiles":[{"name":"Old box","host":"192.168.1.9","ssh_port":22,"ftp_port":21,"enabled":true}]}`
	if rec := request(t, h, http.MethodPost, "/api/v1/profiles/import", []byte(body)); rec.Code != http.StatusCreated {
		t.Fatalf("import = %d %s", rec.Code, rec.Body)
	}
}
