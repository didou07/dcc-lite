package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

func TestRemoteKeyPressesTheMappedKeyOnTheReceiver(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		if _, pass, ok := r.BasicAuth(); !ok || pass != "pw" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"result": true, "message": "ok"}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	post := func(body string) int {
		return request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/remote-key", []byte(body)).Code
	}
	if code := post(`{"key":"OK"}`); code != http.StatusOK {
		t.Fatalf("ok key = %d", code)
	}
	if code := post(`{"key":"menu","long":true}`); code != http.StatusOK {
		t.Fatalf("long key = %d", code)
	}
	want := []string{"/api/remotecontrol?command=352", "/api/remotecontrol?command=139&type=long"}
	if len(*seen) != 2 || (*seen)[0] != want[0] || (*seen)[1] != want[1] {
		t.Fatalf("receiver was asked %v, want %v", *seen, want)
	}
	// A browser can never send a raw code or an unknown name.
	for _, body := range []string{`{"key":"352"}`, `{"key":"../x"}`, `{"key":""}`, `{"code":5}`} {
		if code := post(body); code != http.StatusBadRequest {
			t.Fatalf("%s = %d, want 400", body, code)
		}
	}
	if len(*seen) != 2 {
		t.Fatalf("rejected requests reached the receiver: %v", *seen)
	}
}

func TestRemoteKeyFallsBackToTheOlderWebInterfaceAndReportsRefusals(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/"):
			w.WriteHeader(http.StatusNotFound)
		case r.URL.Query().Get("command") == "116":
			_, _ = w.Write([]byte(`{"result": false, "message": "not allowed"}`))
		default:
			_, _ = w.Write([]byte(`{"result": true}`))
		}
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/remote-key", []byte(`{"key":"up"}`)); rec.Code != http.StatusOK {
		t.Fatalf("fallback = %d %s", rec.Code, rec.Body)
	}
	if got := (*seen)[len(*seen)-1]; got != "/web/remotecontrol?command=103" {
		t.Fatalf("fallback request = %q", got)
	}
	rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/remote-key", []byte(`{"key":"power"}`))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "not allowed") {
		t.Fatalf("refusal = %d %s", rec.Code, rec.Body)
	}
}

func TestDeviceMessageIsValidatedAndEncoded(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result": true}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	send := func(body string) int {
		return request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/message", []byte(body)).Code
	}
	if code := send(`{"text":"Dinner & ready?","seconds":5}`); code != http.StatusOK {
		t.Fatalf("message = %d", code)
	}
	if got := (*seen)[0]; got != "/api/message?text=Dinner+%26+ready%3F&timeout=5&type=1" {
		t.Fatalf("receiver was asked %q", got)
	}
	for _, body := range []string{`{"text":""}`, `{"text":"a\nb"}`, `{"text":"` + strings.Repeat("x", 301) + `"}`} {
		if code := send(body); code != http.StatusUnprocessableEntity {
			t.Fatalf("%.20s = %d, want 422", body, code)
		}
	}
}
