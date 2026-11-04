package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/didou07/dcc-lite/src/app"
	"github.com/didou07/dcc-lite/src/config"
)

func testServer(t *testing.T) http.Handler {
	t.Helper()
	store, err := config.NewProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	application := app.New("test", store, t.TempDir())
	t.Cleanup(application.Terminals.CloseAll)
	t.Cleanup(application.Files.CloseAll)
	return New(application)
}

func request(t *testing.T, handler http.Handler, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "http://127.0.0.1:8787"+path, bytes.NewReader(body))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	return recorder
}

func TestHealth(t *testing.T) {
	h := testServer(t)
	res := request(t, h, http.MethodGet, "/api/v1/health", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("health status %d: %s", res.Code, res.Body)
	}
	if res := request(t, h, http.MethodGet, "/api/v1/modules", nil); res.Code != http.StatusNotFound {
		t.Fatalf("removed modules route status=%d, want 404", res.Code)
	}
	for _, removed := range []string{"/api/v1/openwebif/x/summary", "/api/v1/management/x/packages", "/api/v1/backups", "/api/v1/scripts", "/api/v1/network/interfaces"} {
		if res := request(t, h, http.MethodGet, removed, nil); res.Code != http.StatusNotFound && res.Code != http.StatusOK {
			t.Fatalf("%s status=%d", removed, res.Code)
		} else if res.Code == http.StatusOK && strings.Contains(res.Header().Get("Content-Type"), "json") {
			t.Fatalf("%s must not be served as an API any more", removed)
		}
	}
}

func TestTransferResumeAndRestartRoutesAreRegistered(t *testing.T) {
	h := testServer(t)
	for _, suffix := range []string{"resume", "restart"} {
		res := request(t, h, http.MethodPost, "/api/v1/files/transfers/missing/"+suffix, nil)
		if res.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%s route status = %d, want 422: %s", suffix, res.Code, res.Body)
		}
	}
}

func TestProfileCRUD(t *testing.T) {
	h := testServer(t)
	body := []byte(`{"name":"Box","host":"192.168.1.10","username":"root"}`)
	res := request(t, h, http.MethodPost, "/api/v1/profiles", body)
	if res.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", res.Code, res.Body)
	}
	var result struct {
		Profile config.Profile `json:"profile"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Profile.ID == "" {
		t.Fatal("missing profile id")
	}
	updateBody := []byte(`{"name":"Bedroom","host":"192.168.1.11","ftp_port":2121,"username":"admin"}`)
	res = request(t, h, http.MethodPut, "/api/v1/profiles/"+result.Profile.ID, updateBody)
	if res.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", res.Code, res.Body)
	}
	if err := json.Unmarshal(res.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Profile.Name != "Bedroom" || result.Profile.FTPPort != 2121 {
		t.Fatalf("profile was not updated: %#v", result.Profile)
	}
	res = request(t, h, http.MethodGet, "/api/v1/profiles", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("list status %d", res.Code)
	}
	res = request(t, h, http.MethodDelete, "/api/v1/profiles/"+result.Profile.ID, nil)
	if res.Code != http.StatusNoContent {
		t.Fatalf("delete status %d: %s", res.Code, res.Body)
	}
}

func TestProfilePasswordIsAcceptedButNeverReturned(t *testing.T) {
	h := testServer(t)
	body := []byte(`{"name":"Box","host":"192.168.1.10","password":"local-secret"}`)
	res := request(t, h, http.MethodPost, "/api/v1/profiles", body)
	if res.Code != http.StatusCreated {
		t.Fatalf("expected 201, got %d: %s", res.Code, res.Body)
	}
	if bytes.Contains(res.Body.Bytes(), []byte("local-secret")) {
		t.Fatal("profile response leaked receiver password")
	}
}

func TestRejectsCrossOriginAndNonLoopbackHost(t *testing.T) {
	h := testServer(t)
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787/api/v1/profiles", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Origin", "http://evil.example")
	res := httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("cross-origin status %d", res.Code)
	}

	req = httptest.NewRequest(http.MethodGet, "http://127.0.0.1:8787/api/v1/health", nil)
	req.Host = "192.168.1.20:8787"
	res = httptest.NewRecorder()
	h.ServeHTTP(res, req)
	if res.Code != http.StatusForbidden {
		t.Fatalf("non-loopback host status %d", res.Code)
	}
}

func TestDefaultReceiverCredentialsAndPasswordPersistence(t *testing.T) {
	store, err := config.NewProfileStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	application := app.New("test", store, t.TempDir())
	t.Cleanup(application.Terminals.CloseAll)
	t.Cleanup(application.Files.CloseAll)
	h := New(application)

	create := request(t, h, http.MethodPost, "/api/v1/profiles", []byte(`{"name":"Receiver","host":"192.168.100.33"}`))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status %d: %s", create.Code, create.Body)
	}
	var created struct {
		Profile config.Profile `json:"profile"`
	}
	if err := json.Unmarshal(create.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Profile.Username != "root" {
		t.Fatalf("default username = %q", created.Profile.Username)
	}
	if strings.Contains(create.Body.String(), "dreambox") {
		t.Fatal("API response must not expose password")
	}
	stored, ok := store.Get(created.Profile.ID)
	if !ok || stored.Password != "dreambox" {
		t.Fatalf("default password not stored: %#v", stored)
	}

	list := request(t, h, http.MethodGet, "/api/v1/profiles", nil)
	if list.Code != http.StatusOK {
		t.Fatalf("list status %d", list.Code)
	}
	if !bytes.Contains(list.Body.Bytes(), []byte(`"credential_configured":true`)) || bytes.Contains(list.Body.Bytes(), []byte("dreambox")) {
		t.Fatalf("profile list state or password privacy incorrect: %s", list.Body)
	}

	update := request(t, h, http.MethodPut, "/api/v1/profiles/"+created.Profile.ID, []byte(`{"name":"Receiver","host":"192.168.100.33","username":"root"}`))
	if update.Code != http.StatusOK {
		t.Fatalf("update status %d: %s", update.Code, update.Body)
	}
	stored, _ = store.Get(created.Profile.ID)
	if stored.Password != "dreambox" {
		t.Fatal("blank password on edit should preserve existing password")
	}

	clear := request(t, h, http.MethodDelete, "/api/v1/profiles/"+created.Profile.ID+"/credential", nil)
	if clear.Code != http.StatusNoContent {
		t.Fatalf("clear password status %d: %s", clear.Code, clear.Body)
	}
	stored, _ = store.Get(created.Profile.ID)
	if stored.Password != "" {
		t.Fatal("password was not cleared")
	}
}

func TestLegacyVaultAPIIsRemoved(t *testing.T) {
	h := testServer(t)
	for _, path := range []string{"/api/v1/vault/status", "/api/v1/vault/setup", "/api/v1/vault/unlock", "/api/v1/vault/lock"} {
		res := request(t, h, http.MethodGet, path, nil)
		if res.Code != http.StatusNotFound {
			t.Fatalf("legacy vault endpoint %s should be removed, got %d", path, res.Code)
		}
	}
}

func TestProfileExportAndImportNeverContainSecrets(t *testing.T) {
	h := testServer(t)
	create := request(t, h, http.MethodPost, "/api/v1/profiles", []byte(`{"name":"Receiver","host":"192.168.1.51","username":"root"}`))
	if create.Code != http.StatusCreated {
		t.Fatalf("create status %d", create.Code)
	}
	export := request(t, h, http.MethodGet, "/api/v1/profiles/export", nil)
	if export.Code != http.StatusOK {
		t.Fatalf("export status %d", export.Code)
	}
	var payload struct {
		SchemaVersion int              `json:"schema_version"`
		Profiles      []config.Profile `json:"profiles"`
	}
	if err := json.Unmarshal(export.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.SchemaVersion != config.SchemaVersion || len(payload.Profiles) != 1 {
		t.Fatalf("unexpected export: %#v", payload)
	}
	importBody, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	imported := request(t, h, http.MethodPost, "/api/v1/profiles/import", importBody)
	if imported.Code != http.StatusCreated {
		t.Fatalf("import status %d: %s", imported.Code, imported.Body)
	}
	var result struct {
		Imported int `json:"imported"`
	}
	if err := json.Unmarshal(imported.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Imported != 1 {
		t.Fatalf("expected 1 imported profile, got %d", result.Imported)
	}
}

func TestFileCommanderLocalAPIAndPathConfinement(t *testing.T) {
	h := testServer(t)
	res := request(t, h, http.MethodGet, "/api/v1/files/local/list?path=.", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("local home listing status %d: %s", res.Code, res.Body)
	}
	res = request(t, h, http.MethodGet, "/api/v1/files/local/list?path=../../", nil)
	if res.Code != http.StatusBadRequest {
		t.Fatalf("path traversal listing status %d: %s", res.Code, res.Body)
	}
	res = request(t, h, http.MethodPost, "/api/v1/files/local/mkdir", []byte(`{"path":"../dccnext-stage4-escape"}`))
	if res.Code != http.StatusUnprocessableEntity {
		t.Fatalf("path traversal mkdir status %d: %s", res.Code, res.Body)
	}
	res = request(t, h, http.MethodGet, "/api/v1/files/sessions", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("file session list status %d: %s", res.Code, res.Body)
	}
	var sessions struct {
		Sessions []any `json:"sessions"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &sessions); err != nil {
		t.Fatal(err)
	}
	if len(sessions.Sessions) != 0 {
		t.Fatalf("unexpected sessions: %#v", sessions.Sessions)
	}
}

func TestSecurityHeadersAndEnglishOnlyUI(t *testing.T) {
	h := testServer(t)
	res := request(t, h, http.MethodGet, "/", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("home status %d", res.Code)
	}
	for name, want := range map[string]string{
		"X-Content-Type-Options":            "nosniff",
		"X-Frame-Options":                   "DENY",
		"Referrer-Policy":                   "no-referrer",
		"Cross-Origin-Resource-Policy":      "same-origin",
		"Cross-Origin-Opener-Policy":        "same-origin",
		"X-Permitted-Cross-Domain-Policies": "none",
	} {
		if got := res.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	csp := res.Header().Get("Content-Security-Policy")
	if csp == "" || !contains(csp, "default-src 'self'") || !contains(csp, "frame-ancestors 'none'") || contains(csp, "script-src 'self' 'unsafe") || contains(csp, "default-src 'unsafe") {
		t.Fatalf("unexpected Content-Security-Policy: %q", csp)
	}
	body := res.Body.String()
	for _, required := range []string{`lang="en"`, `id="deviceSelect"`, `id="terminalTabs"`, `id="localPane"`, `id="remotePane"`, `id="transferList"`} {
		if !contains(body, required) {
			t.Errorf("home page missing required Stage 15 control %q", required)
		}
	}
	if contains(body, `i18n.js`) || contains(body, `dir="rtl"`) {
		t.Fatal("obsolete localization or RTL markup remains")
	}
	js := request(t, h, http.MethodGet, "/i18n.js", nil)
	if js.Code != http.StatusNotFound {
		t.Fatalf("removed localization bundle status=%d, want 404", js.Code)
	}
}

func TestClearFinishedFileTransfersEndpoint(t *testing.T) {
	h := testServer(t)
	res := request(t, h, http.MethodDelete, "/api/v1/files/transfers", nil)
	if res.Code != http.StatusOK {
		t.Fatalf("clear transfers status %d: %s", res.Code, res.Body)
	}
	var payload struct {
		Removed int `json:"removed"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Removed != 0 {
		t.Fatalf("removed %d tasks from empty queue, want 0", payload.Removed)
	}
}

func contains(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func TestRemoteTextEditRoutesValidateInput(t *testing.T) {
	h := testServer(t)
	if res := request(t, h, http.MethodGet, "/api/v1/files/sessions/missing/content", nil); res.Code != http.StatusBadRequest {
		t.Fatalf("read without path = %d, want 400: %s", res.Code, res.Body)
	}
	if res := request(t, h, http.MethodGet, "/api/v1/files/sessions/missing/content?path=/etc/hostname", nil); res.Code != http.StatusNotFound {
		t.Fatalf("read on unknown session = %d, want 404: %s", res.Code, res.Body)
	}
	if res := request(t, h, http.MethodPut, "/api/v1/files/sessions/missing/content", []byte(`{"content":"x"}`)); res.Code != http.StatusBadRequest {
		t.Fatalf("write without path = %d, want 400: %s", res.Code, res.Body)
	}
	res := request(t, h, http.MethodPut, "/api/v1/files/sessions/missing/content", []byte(`{"path":"/etc/hostname","content":"x"}`))
	if res.Code != http.StatusNotFound {
		t.Fatalf("write on unknown session = %d, want 404: %s", res.Code, res.Body)
	}
	if res := request(t, h, http.MethodPut, "/api/v1/files/sessions/missing/content", []byte(`{"path":"/a","content":"x","bogus":1}`)); res.Code != http.StatusBadRequest {
		t.Fatalf("unknown field = %d, want 400", res.Code)
	}
}

func TestNetworkScanAPIValidatesRangesAndReturnsList(t *testing.T) {
	h := testServer(t)
	for _, bad := range []string{`{}`, `{"range":"8.8.8.0/24"}`, `{"range":"10.0.0.0/8"}`, `{"range":"nonsense"}`} {
		if res := request(t, h, http.MethodPost, "/api/v1/network/scan", []byte(bad)); res.Code != http.StatusBadRequest {
			t.Errorf("scan %s = %d, want 400: %s", bad, res.Code, res.Body)
		}
	}
	// 127.0.0.2 has nothing listening on the management ports: an empty, well-formed answer.
	res := request(t, h, http.MethodPost, "/api/v1/network/scan", []byte(`{"range":"127.0.0.2"}`))
	if res.Code != http.StatusOK {
		t.Fatalf("scan = %d: %s", res.Code, res.Body)
	}
	var out struct {
		Hosts    []any `json:"hosts"`
		Scanned  int   `json:"scanned"`
		Complete bool  `json:"complete"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &out); err != nil || out.Hosts == nil || out.Scanned != 1 || !out.Complete {
		t.Fatalf("unexpected scan response %s (%v)", res.Body, err)
	}
	if res := request(t, h, http.MethodGet, "/api/v1/network/ranges", nil); res.Code != http.StatusOK || !strings.Contains(res.Body.String(), `"ranges"`) {
		t.Fatalf("ranges = %d %s", res.Code, res.Body)
	}
}
