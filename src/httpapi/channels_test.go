package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const (
	refCh1    = "1:0:19:2B66:3F3:1:C00000:0:0:0:"
	refMarker = "1:64:1:0:0:0:0:0:0:0::Group"
)

func TestDeviceChannelsListsBouquetsAndMarkers(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":true,"services":[{"servicereference":"1:7:1:0:0:0:0:0:0:0:FROM BOUQUET \"b.tv\" ORDER BY bouquet","servicename":"Favourites","subservices":[
			{"servicereference":"` + refMarker + `","servicename":"News","pos":0},
			{"servicereference":"` + refCh1 + `","servicename":" Channel One ","pos":1},
			{"servicereference":"4097:0:1:1:0:0:0:0:0:0:http%3a//x/y:IPTV","servicename":"IPTV","pos":2},
			{"servicereference":"garbage","servicename":"bad","pos":3}]}]}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/channels", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("channels = %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{`"name":"Favourites"`, `"name":"Channel One"`, `"marker":true`, `"picon":true`, `"number":1`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, "garbage") {
		t.Fatalf("an invalid reference was relayed: %s", body)
	}
	if got := (*seen)[0]; got != "/api/getallservices" {
		t.Fatalf("receiver was asked %q", got)
	}
	request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/channels?type=radio", nil)
	if got := (*seen)[1]; got != "/api/getallservices?type=radio" {
		t.Fatalf("radio request = %q", got)
	}
	if rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/channels?type=../x", nil); rec.Code != http.StatusBadRequest || len(*seen) != 2 {
		t.Fatalf("bad type = %d, receiver asked %v", rec.Code, *seen)
	}
}

func TestDeviceChannelsReportsNonJSONAnswer(t *testing.T) {
	host, port, _ := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("<html>nope</html>")) })
	h := testServer(t)
	id := createDevice(t, h, host, port)
	if rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/channels", nil); rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "channel list") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestDeviceZapSendsEscapedReferenceOnly(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"result":true}`)) })
	h := testServer(t)
	id := createDevice(t, h, host, port)
	rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/zap", []byte(`{"ref":"`+refCh1+`"}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("zap = %d %s", rec.Code, rec.Body)
	}
	if want := "/api/zap?sRef=" + url.QueryEscape(refCh1); (*seen)[0] != want {
		t.Fatalf("receiver was asked %q, want %q", (*seen)[0], want)
	}
	n := len(*seen)
	for _, bad := range []string{`{"ref":""}`, `{"ref":"../../etc/passwd"}`, `{"ref":"` + refCh1 + `&command=116"}x`, `{"ref":"` + refMarker + `"}`, `{"ref":"1:0:19:2B66:3F3:1:C00000:0:0:0:a\nb"}`} {
		if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/zap", []byte(bad)); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s accepted: %d", bad, rec.Code)
		}
	}
	if len(*seen) != n {
		t.Fatalf("invalid zaps reached the receiver: %v", *seen)
	}
}

func TestDeviceZapRelaysRefusal(t *testing.T) {
	host, port, _ := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":false,"message":"no such service"}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/zap", []byte(`{"ref":"`+refCh1+`"}`))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "no such service") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestDeviceCurrentSummarisesNowAndNext(t *testing.T) {
	host, port, _ := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"info":{"ref":"` + refCh1 + `","name":"Channel One"},
			"now":{"title":"Evening news","begin_timestamp":1000,"duration_sec":"1800","remaining":600},"next":{"title":"Weather"}}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/current", nil)
	body := rec.Body.String()
	for _, want := range []string{`"name":"Channel One"`, `"title":"Evening news"`, `"duration":1800`, `"remaining":600`, `"title":"Weather"`} {
		if rec.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("missing %s in %d %s", want, rec.Code, body)
		}
	}
}

func TestDevicePiconIsDerivedFromReferenceAndMustBePNG(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nxxxx")
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "1_0_19_2B66_3F3_1_C00000_0_0_0.png") {
			_, _ = w.Write(png)
			return
		}
		_, _ = w.Write([]byte("<html>not an image</html>"))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/picon?ref="+url.QueryEscape(refCh1), nil)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || rec.Body.String() != string(png) {
		t.Fatalf("picon = %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if (*seen)[0] != "/picon/1_0_19_2B66_3F3_1_C00000_0_0_0.png" {
		t.Fatalf("receiver was asked %q", (*seen)[0])
	}
	n := len(*seen)
	for _, ref := range []string{"../../etc/passwd", "", "1:0:19:2B66:3F3:1:C00000:0:0", "1:0:19:2B66:3F3:1:C00000:0:0:/../x:"} {
		if rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/picon?ref="+url.QueryEscape(ref), nil); rec.Code != http.StatusNoContent {
			t.Fatalf("ref %q = %d", ref, rec.Code)
		}
	}
	if len(*seen) != n {
		t.Fatalf("bad references reached the receiver: %v", *seen)
	}
	// Anything that is not a PNG is never relayed.
	if rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/picon?ref="+url.QueryEscape("1:0:19:2B66:3F3:1:C00001:0:0:0:"), nil); rec.Code != http.StatusNoContent {
		t.Fatalf("non-PNG relayed: %d", rec.Code)
	}
}

func TestDevicePowerActions(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":true,"instandby":true}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	for action, want := range map[string]string{"standby": "/api/powerstate?newstate=5", "wakeup": "/api/powerstate?newstate=4", "deep": "/api/powerstate?newstate=1",
		"reboot": "/api/powerstate?newstate=2", "restart_gui": "/api/powerstate?newstate=3", "toggle": "/api/powerstate?newstate=0", "reload": "/api/servicelistreload?mode=0"} {
		rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/power", []byte(`{"action":"`+action+`"}`))
		if rec.Code != http.StatusOK || (*seen)[len(*seen)-1] != want {
			t.Fatalf("%s = %d, receiver asked %v", action, rec.Code, *seen)
		}
	}
	n := len(*seen)
	for _, bad := range []string{`{"action":"7"}`, `{"action":"standby&x=1"}`, `{}`} {
		if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/power", []byte(bad)); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s accepted", bad)
		}
	}
	if len(*seen) != n {
		t.Fatal("invalid action reached the receiver")
	}
}
