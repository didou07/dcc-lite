package httpapi

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func makeZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte(content))
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func (e *bouquetEnv) upload(t *testing.T, endpoint, filename string, data []byte) (int, map[string]any) {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, _ := mw.CreateFormFile("file", filename)
	_, _ = part.Write(data)
	_ = mw.Close()
	target := e.url + endpoint
	if strings.HasPrefix(endpoint, "/") {
		target = endpoint
	}
	req := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:8787"+target, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func zipNames(t *testing.T, data []byte) map[string]string {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range zr.File {
		rc, _ := f.Open()
		b, _ := io.ReadAll(rc)
		rc.Close()
		out[f.Name] = string(b)
	}
	return out
}

func TestBackupHoldsChannelFilesOnlyUnlessAllIsAsked(t *testing.T) {
	e := newBouquetEnv(t)
	e.fs.Set("/etc/enigma2/settings", "config.misc=1\n", 0o644)
	e.fs.Set("/etc/enigma2/.hidden", "x", 0o644)
	res := request(t, e.h, http.MethodGet, e.url+"backup", nil)
	if res.Code != http.StatusOK || res.Header().Get("Content-Type") != "application/zip" || !strings.Contains(res.Header().Get("Content-Disposition"), "enigma2-channels-127.0.0.1-") {
		t.Fatalf("backup = %d %v", res.Code, res.Header())
	}
	files := zipNames(t, res.Body.Bytes())
	for _, want := range []string{"lamedb", "bouquets.tv", "userbouquet.favourites.tv", "userbouquet.news.tv", backupManifest} {
		if _, ok := files[want]; !ok {
			t.Errorf("%s missing from %v", want, files)
		}
	}
	if _, ok := files["settings"]; ok {
		t.Error("channels backup holds the settings file")
	}
	if files["lamedb"] != lamedb {
		t.Error("lamedb content changed")
	}
	all := zipNames(t, request(t, e.h, http.MethodGet, e.url+"backup?scope=all", nil).Body.Bytes())
	if all["settings"] == "" || all[".hidden"] != "" {
		t.Errorf("all-scope backup = %v", all)
	}
}

func TestRestoreWritesOnlyChannelFilesAndKeepsTheRestAsIs(t *testing.T) {
	e := newBouquetEnv(t)
	e.fs.Set("/etc/enigma2/settings", "keep me\n", 0o644)
	archive := makeZip(t, map[string]string{
		"lamedb":                  "NEW LAMEDB\n",
		"bouquets.tv":             "#NAME New index\n",
		"userbouquet.sports.tv":   "#NAME Sports\n",
		"settings":                "overwrite attempt\n",
		"../escape":               "x",
		"sub/lamedb":              "x",
		"userbouquet.bad name.tv": "x",
		backupManifest:            "{}",
	})
	code, out := e.upload(t, "restore", "backup.zip", archive)
	if code != http.StatusOK {
		t.Fatalf("restore = %d %v", code, out)
	}
	if got, _ := e.fs.Get("/etc/enigma2/lamedb"); got != "NEW LAMEDB\n" {
		t.Fatalf("lamedb = %q", got)
	}
	if got, _ := e.fs.Get("/etc/enigma2/userbouquet.sports.tv"); got != "#NAME Sports\n" {
		t.Fatalf("new bouquet = %q", got)
	}
	if got, _ := e.fs.Get("/etc/enigma2/settings"); got != "keep me\n" {
		t.Fatalf("settings changed to %q", got)
	}
	if _, ok := e.fs.Get("/etc/escape"); ok {
		t.Fatal("a path outside the folder was written")
	}
	for _, name := range e.fs.Names() {
		if strings.Contains(name, ".dccnext-") || strings.Contains(name, ".part") {
			t.Fatalf("temporary file left behind: %s", name)
		}
	}
	restored := out["restored"].([]any)
	if len(restored) != 3 || restored[len(restored)-1] != "bouquets.tv" {
		t.Fatalf("restored = %v (the bouquet index must be written last)", restored)
	}
	if skipped := out["skipped"].([]any); len(skipped) != 4 {
		t.Fatalf("skipped = %v", skipped)
	}
}

func TestRestoreRejectsFilesThatAreNotChannelBackups(t *testing.T) {
	e := newBouquetEnv(t)
	for name, data := range map[string][]byte{
		"not a zip":    []byte("hello"),
		"no list file": makeZip(t, map[string]string{"settings": "x", "userbouquet.a.tv": "x"}),
		"empty":        {},
	} {
		if code, out := e.upload(t, "restore", "b.zip", data); code != http.StatusUnprocessableEntity {
			t.Errorf("%s: status %d %v", name, code, out)
		}
	}
	if got, _ := e.fs.Get("/etc/enigma2/lamedb"); got != lamedb {
		t.Fatal("a rejected restore changed the receiver")
	}
}

func TestIPKUploadGoesToTmpAndReturnsInstallCommand(t *testing.T) {
	e := newBouquetEnv(t)
	code, out := e.upload(t, "/api/v1/files/sessions/sess0001/ipk", "enigma2-plugin-extensions-foo_1.0_all.ipk", []byte("!<arch>\ndebian"))
	if code != http.StatusOK {
		t.Fatalf("ipk = %d %v", code, out)
	}
	want := "/tmp/enigma2-plugin-extensions-foo_1.0_all.ipk"
	if out["path"] != want || out["command"] != "opkg install "+want+" && rm -f "+want {
		t.Fatalf("answer = %v", out)
	}
	if got, ok := e.fs.Get(want); !ok || got != "!<arch>\ndebian" {
		t.Fatalf("file on receiver = %q, %v", got, ok)
	}
	// The browser's folder part of a name is dropped, so nothing lands outside /tmp.
	if code, out := e.upload(t, "/api/v1/files/sessions/sess0001/ipk", "../../etc/x.ipk", []byte("x")); code != http.StatusOK || out["path"] != "/tmp/x.ipk" {
		t.Fatalf("path name = %d %v", code, out)
	}
	for _, bad := range []string{"evil.sh", "a;b.ipk", ".ipk"} {
		if code, _ := e.upload(t, "/api/v1/files/sessions/sess0001/ipk", bad, []byte("x")); code != http.StatusUnprocessableEntity {
			t.Errorf("%q accepted (status %d)", bad, code)
		}
	}
}

func TestDeviceInfoReadsOpenWebifAboutAndSignal(t *testing.T) {
	about := map[string]any{"info": map[string]any{
		"brand": "Vu+", "model": "Duo 4K", "friendlyimagedistro": "OpenATV", "imagever": "7.5.1",
		"enigmaver": "2024-01-01", "kernelver": "4.1.45", "friendlychipsettext": "Broadcom 7278",
		"uptime": "2d 03:10", "mem3": "1 GB free / 2 GB total",
		"tuners": []any{map[string]any{"name": "Tuner A", "type": "DVB-S2", "live": "19.2E / Das Erste", "rec": "", "stream": ""}},
		"hdd":    []any{map[string]any{"model": "WDC", "mount": "/media/hdd", "friendlycapacity": "100 GB free / 500 GB total"}},
	}}
	signal := map[string]any{"snr": "78", "snr_db": "12.50", "agc": float64(91), "ber": "", "tunertype": "DVB-S2", "tunernumber": float64(0), "inStandby": "false"}
	info := buildDeviceInfo(about, signal)
	if info.Model != "Vu+ Duo 4K" || info.Image != "OpenATV 7.5.1" || info.Memory == "" || len(info.Tuners) != 1 || info.Tuners[0].Live == "" || len(info.Disks) != 1 {
		t.Fatalf("info = %+v", info)
	}
	if info.Signal == nil || info.Signal.SNR != 78 || info.Signal.AGC != 91 || info.Signal.SNRdB != "12.50" || info.Signal.Tuner != "DVB-S2 #0" {
		t.Fatalf("signal = %+v", info.Signal)
	}
	// A receiver in standby reports empty values: no signal bars then.
	if got := buildDeviceInfo(about, map[string]any{"snr": "", "agc": ""}); got.Signal != nil {
		t.Fatalf("standby signal = %+v", got.Signal)
	}
	if got := buildDeviceInfo(map[string]any{}, nil); len(got.Tuners) != 0 || got.Model != "" {
		t.Fatalf("empty = %+v", got)
	}
}

func TestParseTemperature(t *testing.T) {
	for in, want := range map[string]int{"45\n": 45, "45.6": 46, " 60 ": 60} {
		if got, ok := parseTemperature([]byte(in)); !ok || got != want {
			t.Errorf("%q = %d, %v", in, got, ok)
		}
	}
	for _, bad := range []string{"", "abc", "0", "999", "-5"} {
		if _, ok := parseTemperature([]byte(bad)); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDeviceInfoEndpointAndTemperatureFromOpenFileSession(t *testing.T) {
	e := newBouquetEnv(t)
	e.fs.Set("/proc/stb/sensors/temp0/value", "47\n", 0o444)
	host, port, _ := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/about":
			_, _ = w.Write([]byte(`{"info":{"brand":"Vu+","model":"Zero"}}`))
		case "/api/tunersignal":
			_, _ = w.Write([]byte(`{"snr":55,"agc":60,"ber":0}`))
		default:
			http.NotFound(w, r)
		}
	})
	// The device the file session belongs to must point at the fake receiver.
	profiles := e.app.Profiles.List()
	p := profiles[0]
	p.Host, p.Port = host, port
	if _, err := e.app.Profiles.Save(p); err != nil {
		t.Fatal(err)
	}
	res := request(t, e.h, http.MethodGet, "/api/v1/profiles/"+p.ID+"/info", nil)
	var info deviceInfo
	if err := json.Unmarshal(res.Body.Bytes(), &info); err != nil || res.Code != http.StatusOK {
		t.Fatalf("info = %d %s", res.Code, res.Body.String())
	}
	if info.Model != "Vu+ Zero" || info.Signal == nil || info.Signal.SNR != 55 || info.Temperature == nil || *info.Temperature != 47 {
		t.Fatalf("info = %+v temp=%v", info, info.Temperature)
	}
}

func TestStreamPlaylistUsesReceiverAnswerThenFallsBack(t *testing.T) {
	ref := "1:0:19:2B66:3F3:1:C00000:0:0:0:"
	answer := "#EXTM3U \n#EXTVLCOPT:http-reconnect=true \n#EXTINF:-1,Das Erste\nhttp://box:8001/" + ref + "\n"
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/web/stream.m3u" && r.URL.Query().Get("ref") == ref {
			_, _ = w.Write([]byte(answer))
			return
		}
		http.NotFound(w, r)
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	u := "/api/v1/profiles/" + id + "/stream.m3u?ref=" + url.QueryEscape(ref) + "&name=" + url.QueryEscape("Das Erste HD")
	res := request(t, h, http.MethodGet, u, nil)
	if res.Code != http.StatusOK || !strings.HasPrefix(res.Header().Get("Content-Disposition"), `attachment; filename="Das_Erste_HD.m3u"`) || !strings.Contains(res.Body.String(), "http://box:8001/"+ref) {
		t.Fatalf("stream = %d %v %q", res.Code, res.Header(), res.Body.String())
	}
	if len(*seen) != 1 || !strings.Contains((*seen)[0], "/web/stream.m3u?ref=") {
		t.Fatalf("receiver was asked %v", *seen)
	}
	// Another channel the receiver does not answer for: the standard address is written.
	other := "1:0:1:6DCA:44D:1:C00000:0:0:0:"
	res = request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/stream.m3u?ref="+url.QueryEscape(other)+"&name=ZDF", nil)
	if !strings.Contains(res.Body.String(), "#EXTM3U\n") || !strings.Contains(res.Body.String(), "#EXTINF:-1,ZDF\nhttp://127.0.0.1:8001/"+other+"\n") {
		t.Fatalf("fallback = %q", res.Body.String())
	}
	// Anything that is not a channel reference never reaches the receiver.
	n := len(*seen)
	if res := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/stream.m3u?ref=../x", nil); res.Code != http.StatusBadRequest || len(*seen) != n {
		t.Fatalf("bad ref = %d", res.Code)
	}
}
