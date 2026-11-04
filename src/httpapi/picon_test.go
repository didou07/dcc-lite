package httpapi

import (
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestPiconCandidatesFollowEnigma2Fallbacks(t *testing.T) {
	got := piconCandidates("1:0:19:2B66:3F3:1:C00000:0:0:0:", "BBC One HD")
	want := []string{"1_0_19_2B66_3F3_1_C00000_0_0_0.png", "1_0_1_2B66_3F3_1_C00000_0_0_0.png", "bbconehd.png", "bbcone.png"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("candidates = %v, want %v", got, want)
	}
	// Radio keeps its own type; a type 1 service has no extra variant.
	if got := piconCandidates("1:0:2:1:1:1:C00000:0:0:0:", ""); len(got) != 1 {
		t.Fatalf("radio candidates = %v", got)
	}
	if got := piconCandidates("1:0:1:1:1:1:C00000:0:0:0:", "x"); len(got) != 2 {
		t.Fatalf("type 1 candidates = %v", got)
	}
	if got := piconCandidates("../../etc/passwd", "x"); got != nil {
		t.Fatalf("bad reference gave %v", got)
	}
}

func TestPiconNameSimplification(t *testing.T) {
	for in, want := range map[string]string{
		"Canal+ Séries": "canalplusseries", "Sport & News": "sportandnews", "Ünal TV*": "unaltvstar",
		"الجزيرة": "", "  ": "", "../x": "x", "TF1 HD": "tf1hd",
	} {
		if got := piconName(in); got != want {
			t.Errorf("piconName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDevicePiconFallsBackToChannelNameAndRemembersIt(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\nxxxx")
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/picon/channelone.png" {
			_, _ = w.Write(png)
			return
		}
		http.NotFound(w, r)
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	u := "/api/v1/profiles/" + id + "/picon?ref=" + url.QueryEscape(refCh1) + "&name=" + url.QueryEscape("Channel One")
	rec := request(t, h, http.MethodGet, u, nil)
	if rec.Code != http.StatusOK || rec.Body.String() != string(png) {
		t.Fatalf("picon = %d", rec.Code)
	}
	if len(*seen) != 3 || (*seen)[2] != "/picon/channelone.png" {
		t.Fatalf("receiver was asked %v", *seen)
	}
	// The second time only the file that worked is requested.
	request(t, h, http.MethodGet, u, nil)
	if len(*seen) != 4 || (*seen)[3] != "/picon/channelone.png" {
		t.Fatalf("second request asked %v", *seen)
	}
	// A channel with no logo anywhere is remembered, so the receiver is not asked again.
	miss := "/api/v1/profiles/" + id + "/picon?ref=" + url.QueryEscape("1:0:1:9:9:9:C00000:0:0:0:") + "&name=Nothing"
	request(t, h, http.MethodGet, miss, nil)
	n := len(*seen)
	if rec := request(t, h, http.MethodGet, miss, nil); rec.Code != http.StatusNoContent || len(*seen) != n {
		t.Fatalf("miss was not remembered: %d, %v", rec.Code, *seen)
	}
}

func TestDeviceEPGNowMapsRunningProgrammes(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"events":[{"sref":"` + refCh1 + `","title":" Evening news ","begin_timestamp":1000,"duration_sec":1800,"now_timestamp":1200},
			{"sref":"garbage","title":"x"},{"sref":"1:0:19:1:1:1:C00000:0:0:0:","title":""}]}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	bq := "1:7:1:0:0:0:0:0:0:0:FROM BOUQUET \"b.tv\" ORDER BY bouquet"
	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/epgnow?bouquet="+url.QueryEscape(bq), nil)
	body := rec.Body.String()
	for _, want := range []string{`"title":"Evening news"`, `"begin":1000`, `"duration":1800`, `"now":1200`} {
		if rec.Code != http.StatusOK || !strings.Contains(body, want) {
			t.Fatalf("missing %s in %d %s", want, rec.Code, body)
		}
	}
	if strings.Contains(body, "garbage") {
		t.Fatalf("invalid reference relayed: %s", body)
	}
	if want := "/api/epgnow?bRef=" + url.QueryEscape(bq); (*seen)[0] != want {
		t.Fatalf("receiver was asked %q", (*seen)[0])
	}
	n := len(*seen)
	if rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/epgnow?bouquet=../x", nil); rec.Code != http.StatusBadRequest || len(*seen) != n {
		t.Fatalf("bad bouquet = %d", rec.Code)
	}
}
