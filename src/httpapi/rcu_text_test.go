package httpapi

import (
	"net/http"
	"testing"
)

func TestDeviceRemoteTextTypesPlainText(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":true,"message":"RC command text 'x' has been issued"}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)

	rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/remote-text", []byte(`{"text":"News 24 & 100%"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("type = %d %s", rec.Code, rec.Body)
	}
	if got, want := (*seen)[len(*seen)-1], "/api/remotecontrol?text=News+24+%26+100%2525"; got != want {
		t.Fatalf("receiver was asked %q, want %q", got, want)
	}

	n := len(*seen)
	long := make([]byte, 201)
	for i := range long {
		long[i] = 'a'
	}
	for _, bad := range []string{`{}`, `{"text":""}`, `{"text":"line\nbreak"}`, `{"text":"café"}`, `{"text":"` + string(long) + `"}`} {
		if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/remote-text", []byte(bad)); rec.Code != http.StatusUnprocessableEntity {
			t.Fatalf("%.40s accepted: %d", bad, rec.Code)
		}
	}
	if len(*seen) != n {
		t.Fatal("refused text reached the receiver")
	}
}

func TestDeviceRemoteTextReportsUnsupportedImages(t *testing.T) {
	host, port, _ := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":false,"message":"RC command text not supported"}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/remote-text", []byte(`{"text":"abc"}`)); rec.Code != http.StatusBadGateway {
		t.Fatalf("unsupported = %d %s", rec.Code, rec.Body)
	}
}
