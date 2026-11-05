package httpapi

import (
	"net/http"
	"net/url"
	"strings"
	"testing"
)

const refMovie = "1:0:0:0:0:0:0:0:0:0:/media/hdd/movie/20260101 - One - News.ts"

func TestDeviceEPGListsEventsAndDropsEmptyOnes(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":true,"events":[
			{"id":4321,"begin_timestamp":2000,"duration_sec":"1800","title":" Evening news ","shortdesc":"Headlines","longdesc":"All the news","sref":"` + refCh1 + `","sname":"Channel One","now_timestamp":2100},
			{"id":0,"begin_timestamp":0,"duration_sec":0,"title":"N/A","sref":"` + refCh1 + `"},
			{"id":4322,"begin_timestamp":3800,"duration_sec":3600,"title":"Weather","sref":"` + refCh1 + `"}]}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/epg?ref="+url.QueryEscape(refCh1), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("epg = %d %s", rec.Code, rec.Body)
	}
	body := rec.Body.String()
	for _, want := range []string{`"title":"Evening news"`, `"id":4321`, `"duration":1800`, `"title":"Weather"`, `"now":2100`, `"channel":"Channel One"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, "N/A") {
		t.Fatalf("an empty event was relayed: %s", body)
	}
	if want := "/api/epgservice?sRef=" + url.QueryEscape(refCh1); (*seen)[0] != want {
		t.Fatalf("receiver was asked %q, want %q", (*seen)[0], want)
	}
	n := len(*seen)
	for _, bad := range []string{"", "garbage", url.QueryEscape(refMarker)} {
		if rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/epg?ref="+bad, nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("ref %q accepted: %d", bad, rec.Code)
		}
	}
	if len(*seen) != n {
		t.Fatalf("invalid references reached the receiver: %v", *seen)
	}
}

func TestDeviceEPGSearchEscapesTheQuery(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":true,"events":[{"id":7,"begin_timestamp":5000,"duration_sec":600,"title":"Cooking show","sref":"` + refCh1 + `","sname":"Channel One"}]}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/epgsearch?q="+url.QueryEscape("cook & fry"), nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"title":"Cooking show"`) {
		t.Fatalf("search = %d %s", rec.Code, rec.Body)
	}
	if want := "/api/epgsearch?search=" + url.QueryEscape("cook & fry"); (*seen)[0] != want {
		t.Fatalf("receiver was asked %q, want %q", (*seen)[0], want)
	}
	n := len(*seen)
	if rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/epgsearch?q=a", nil); rec.Code != http.StatusBadRequest || len(*seen) != n {
		t.Fatalf("a one letter search = %d, receiver asked %v", rec.Code, *seen)
	}
}

func TestDeviceTimersListsAndNormalises(t *testing.T) {
	host, port, _ := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":true,"timers":[
			{"serviceref":"` + refCh1 + `","servicename":"Channel One","name":"Film","description":"A film","begin":10000,"end":"13600","eit":55,"disabled":0,"justplay":0,"state":0,"repeated":0,"afterevent":3,"filename":null},
			{"serviceref":"` + refCh1 + `","servicename":"Channel One","name":"Reminder","begin":20000,"end":20600,"disabled":1,"justplay":1,"state":3},
			{"serviceref":"garbage","name":"Bad","begin":1,"end":2}]}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/timers", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("timers = %d %s", rec.Code, body)
	}
	for _, want := range []string{`"title":"Film"`, `"end":13600`, `"eit":55`, `"disabled":true`, `"justplay":true`, `"state":3`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, "garbage") {
		t.Fatalf("an invalid timer was relayed: %s", body)
	}
}

func TestDeviceTimerAddByEventAndByTimes(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"result":true}`)) })
	h := testServer(t)
	id := createDevice(t, h, host, port)
	post := func(body string) int {
		return request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/timers", []byte(body)).Code
	}
	if code := post(`{"ref":"` + refCh1 + `","eventid":4321,"afterevent":9}`); code != http.StatusOK {
		t.Fatalf("add by event = %d", code)
	}
	if want := "/api/timeraddbyeventid?sRef=" + url.QueryEscape(refCh1) + "&eventid=4321&justplay=0&afterevent=3"; (*seen)[0] != want {
		t.Fatalf("receiver was asked %q, want %q", (*seen)[0], want)
	}
	if code := post(`{"ref":"` + refCh1 + `","name":"Film & more","begin":1000,"end":4600,"justplay":true,"afterevent":1}`); code != http.StatusOK {
		t.Fatalf("add by times = %d", code)
	}
	if got := (*seen)[1]; !strings.HasPrefix(got, "/api/timeradd?sRef=") || !strings.Contains(got, "&name=Film+%26+more") || !strings.Contains(got, "&justplay=1&afterevent=1") {
		t.Fatalf("timeradd request = %q", got)
	}
	n := len(*seen)
	for _, bad := range []string{
		`{"ref":"` + refMarker + `","eventid":1}`,
		`{"ref":"garbage","eventid":1}`,
		`{"ref":"` + refCh1 + `","name":"","begin":1000,"end":4600}`,
		`{"ref":"` + refCh1 + `","name":"x","begin":4600,"end":1000}`,
		`{"ref":"` + refCh1 + `","name":"x","begin":0,"end":1000}`,
		`{"ref":"` + refCh1 + `","name":"x","begin":1000,"end":999999999}`,
	} {
		if code := post(bad); code != http.StatusBadRequest {
			t.Fatalf("%s accepted: %d", bad, code)
		}
	}
	if len(*seen) != n {
		t.Fatalf("invalid timers reached the receiver: %v", *seen)
	}
}

func TestDeviceTimerToggleDeleteAndCleanup(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"result":true}`)) })
	h := testServer(t)
	id := createDevice(t, h, host, port)
	body := `{"ref":"` + refCh1 + `","begin":1000,"end":4600,"eit":55}`
	if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/timers/toggle", []byte(body)); rec.Code != http.StatusOK {
		t.Fatalf("toggle = %d %s", rec.Code, rec.Body)
	}
	if want := "/api/timertogglestatus?sRef=" + url.QueryEscape(refCh1) + "&begin=1000&end=4600"; (*seen)[0] != want {
		t.Fatalf("toggle asked %q, want %q", (*seen)[0], want)
	}
	if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/timers/delete", []byte(body)); rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if want := "/api/timerdelete?sRef=" + url.QueryEscape(refCh1) + "&begin=1000&end=4600&eit=55"; (*seen)[1] != want {
		t.Fatalf("delete asked %q, want %q", (*seen)[1], want)
	}
	if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/timers/cleanup", nil); rec.Code != http.StatusOK || (*seen)[2] != "/api/timercleanup" {
		t.Fatalf("cleanup = %d, asked %v", rec.Code, *seen)
	}
	n := len(*seen)
	if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/timers/delete", []byte(`{"ref":"x","begin":1,"end":2}`)); rec.Code != http.StatusBadRequest || len(*seen) != n {
		t.Fatalf("bad delete = %d, receiver asked %v", rec.Code, *seen)
	}
}

func TestDeviceTimerAddRelaysConflict(t *testing.T) {
	host, port, _ := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"result":false,"message":"Conflicting Timer(s) detected! Film"}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/timers", []byte(`{"ref":"`+refCh1+`","eventid":9}`))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "Conflicting Timer") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestDeviceMoviesListsRecordingsAndFolders(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"directory":"/media/hdd/movie/","bookmarks":["Kids","a/b"],"movies":[
			{"serviceref":"` + refMovie + `","filename":"/media/hdd/movie/20260101 - One - News.ts","eventname":"News","servicename":"Channel One","recordingtime":1767225600,"length":"45:10","lastseen":12,"filesize":1073741824,"description":"Evening"},
			{"serviceref":"1:0:0:0:0:0:0:0:0:0:/media/hdd/movie/../../etc/x.ts","filename":"/media/hdd/movie/../../etc/x.ts","eventname":"Bad"}]}`))
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/movies", nil)
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("movies = %d %s", rec.Code, body)
	}
	for _, want := range []string{`"title":"News"`, `"length":"45:10"`, `"size":1073741824`, `"folders":["Kids"]`, `"directory":"/media/hdd/movie/"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %s in %s", want, body)
		}
	}
	if strings.Contains(body, "etc/x.ts") {
		t.Fatalf("a path with .. was relayed: %s", body)
	}
	if (*seen)[0] != "/api/movielist" {
		t.Fatalf("receiver was asked %q", (*seen)[0])
	}
	request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/movies?dir="+url.QueryEscape("/media/hdd/movie/Kids/"), nil)
	if want := "/api/movielist?dirname=" + url.QueryEscape("/media/hdd/movie/Kids/"); (*seen)[1] != want {
		t.Fatalf("folder request = %q, want %q", (*seen)[1], want)
	}
	n := len(*seen)
	for _, bad := range []string{"relative", "/media/../etc"} {
		if rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/movies?dir="+url.QueryEscape(bad), nil); rec.Code != http.StatusBadRequest {
			t.Fatalf("dir %q accepted: %d", bad, rec.Code)
		}
	}
	if len(*seen) != n {
		t.Fatalf("invalid folders reached the receiver: %v", *seen)
	}
}

func TestDeviceMovieDeleteChecksTheReference(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{"result":true}`)) })
	h := testServer(t)
	id := createDevice(t, h, host, port)
	if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/movies/delete", []byte(`{"ref":"`+refMovie+`"}`)); rec.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", rec.Code, rec.Body)
	}
	if want := "/api/moviedelete?sRef=" + url.QueryEscape(refMovie); (*seen)[0] != want {
		t.Fatalf("receiver was asked %q, want %q", (*seen)[0], want)
	}
	n := len(*seen)
	for _, bad := range []string{
		`{"ref":"` + refCh1 + `"}`, // a channel, not a recording
		`{"ref":"1:0:0:0:0:0:0:0:0:0:/media/../etc/passwd"}`,
		`{"ref":"1:0:0:0:0:0:0:0:0:0:relative.ts"}`,
		`{"ref":""}`,
	} {
		if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/movies/delete", []byte(bad)); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s accepted: %d", bad, rec.Code)
		}
	}
	if len(*seen) != n {
		t.Fatalf("invalid deletes reached the receiver: %v", *seen)
	}
}

func TestDeviceMovieStreamUsesReceiverPlaylistThenFallsBack(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/web/ts.m3u") {
			_, _ = w.Write([]byte("#EXTM3U\n#EXTINF:-1,News\nhttp://box:80/file?file=x\n"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/movie.m3u?name=News&file="+url.QueryEscape("/media/hdd/movie/a b.ts"), nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "http://box:80/file?file=x") || !strings.Contains(rec.Header().Get("Content-Disposition"), "News.m3u") {
		t.Fatalf("got %d %q %v", rec.Code, rec.Body, rec.Header())
	}
	if want := "/web/ts.m3u?file=" + url.QueryEscape("/media/hdd/movie/a b.ts"); (*seen)[0] != want {
		t.Fatalf("receiver was asked %q, want %q", (*seen)[0], want)
	}
	// A receiver that gives no playlist still gets a working one.
	host2, port2, _ := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	id2 := createDevice(t, h, host2, port2)
	rec = request(t, h, http.MethodGet, "/api/v1/profiles/"+id2+"/movie.m3u?file="+url.QueryEscape("/media/hdd/movie/a b.ts"), nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "/file?file=%2Fmedia%2Fhdd%2Fmovie%2Fa+b.ts") {
		t.Fatalf("fallback = %d %q", rec.Code, rec.Body)
	}
	if rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/movie.m3u?file=../etc/passwd", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("a relative path was accepted: %d", rec.Code)
	}
}
