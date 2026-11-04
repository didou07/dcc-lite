package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/config"
)

type fakeRemote struct {
	settings app.RemoteSettings
	password string
}

func (f *fakeRemote) Status() app.RemoteStatus {
	return app.RemoteStatus{RemoteSettings: f.settings, Managed: true, Running: f.settings.Enabled}
}
func (f *fakeRemote) Apply(in app.RemoteSettings) (app.RemoteStatus, string, error) {
	f.settings = in
	return f.Status(), "", nil
}
func (f *fakeRemote) SetPassword(p string) error { f.password = p; return nil }
func (f *fakeRemote) GeneratePassword() (string, error) {
	f.password = "generated"
	return f.password, nil
}

func localAdminServer(t *testing.T) (http.Handler, *fakeRemote) {
	t.Helper()
	store, err := config.NewProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	application := app.New("test", store, t.TempDir())
	t.Cleanup(application.Terminals.CloseAll)
	t.Cleanup(application.Files.CloseAll)
	fake := &fakeRemote{}
	application.RemoteAccess = fake
	return New(application), fake
}

func localDo(h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "http://127.0.0.1:8787"+target, strings.NewReader(body))
	req.Host = "127.0.0.1:8787"
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRemoteAdminOnLoopback(t *testing.T) {
	h, fake := localAdminServer(t)
	if rec := localDo(h, "PUT", "/api/v1/remote-access", `{"enabled":true,"listen":"0.0.0.0:8788","tls_hosts":"1.2.3.4"}`); rec.Code != 200 || !fake.settings.Enabled {
		t.Fatalf("put: %d %s", rec.Code, rec.Body)
	}
	var got struct{ Status app.RemoteStatus }
	rec := localDo(h, "GET", "/api/v1/remote-access", "")
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Status.Listen != "0.0.0.0:8788" {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	if rec := localDo(h, "POST", "/api/v1/remote-access/password", `{"generate":true}`); rec.Code != 200 || !strings.Contains(rec.Body.String(), "generated") {
		t.Fatalf("generate: %d %s", rec.Code, rec.Body)
	}
	if rec := localDo(h, "POST", "/api/v1/remote-access/password", `{"password":"x","generate":true}`); rec.Code != 400 {
		t.Fatalf("both fields accepted: %d", rec.Code)
	}
	if rec := localDo(h, "POST", "/api/v1/remote-access/password", `{}`); rec.Code != 400 {
		t.Fatalf("empty request accepted: %d", rec.Code)
	}
}

func TestRemoteAdminIsNotReachableOverHTTPS(t *testing.T) {
	handler, application := remoteTestServerWithApp(t)
	application.RemoteAccess = &fakeRemote{}
	login := remoteDo(handler, http.MethodPost, "/api/v1/auth/login", []byte(`{"password":"`+remotePassword+`"}`), sameOrigin)
	if login.Code != 200 {
		t.Fatalf("login: %d", login.Code)
	}
	cookie := login.Result().Cookies()
	for _, c := range []struct{ method, path string }{{"GET", "/api/v1/remote-access"}, {"PUT", "/api/v1/remote-access"}, {"POST", "/api/v1/remote-access/password"}} {
		rec := remoteDo(handler, c.method, c.path, []byte(`{"enabled":false,"listen":"0.0.0.0:8788"}`), func(r *http.Request) {
			sameOrigin(r)
			for _, k := range cookie {
				r.AddCookie(k)
			}
		})
		if rec.Code == 200 {
			t.Errorf("%s %s reachable by a signed-in remote visitor", c.method, c.path)
		}
	}
}
