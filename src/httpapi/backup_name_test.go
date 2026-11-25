package httpapi

import (
	"testing"
	"time"
)

func TestParseImageProbe(t *testing.T) {
	out := "##/etc/image-version\r\nbox_type=dm900\r\nbuild_type=1\r\ndistro=openatv\r\nversion=7.4\r\nbuild=1234\r\n##/etc/issue\r\nWelcome to Openplus for %h\r\n##/proc/stb/info/boxtype\r\n##/proc/stb/info/model\r\ndm900\r\n"
	want := imageIdentity{Name: "openatv", Version: "7.4", Build: "1234", Box: "dm900"}
	if got := parseImageProbe(out); got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestParseImageProbeFallbacks(t *testing.T) {
	out := "##/etc/image-version\n##/etc/issue\nWelcome to Gemini 4.2 \\n \\l\n##/proc/stb/info/boxtype\n##/proc/stb/info/model\ndm900\n"
	got := parseImageProbe(out)
	if got.Name != "Gemini" || got.Box != "dm900" || got.Version != "" {
		t.Fatalf("got %+v", got)
	}
}

func TestBackupArchiveName(t *testing.T) {
	at := time.Date(2026, 11, 25, 21, 3, 0, 0, time.UTC)
	cases := []struct {
		id   imageIdentity
		host string
		want string
	}{
		{imageIdentity{"OpenATV", "7.4", "1234", "dm900"}, "192.168.1.33", "OpenATV_v7.4_b1234_dm900_2026-11-25_21-03.tar.gz"},
		{imageIdentity{"Gemini 4.2", "", "", "dm900"}, "192.168.1.33", "Gemini-4.2_dm900_2026-11-25_21-03.tar.gz"},
		{imageIdentity{}, "192.168.1.33", "Receiver_192.168.1.33_2026-11-25_21-03.tar.gz"},
		{imageIdentity{"../x;y", "1/2", "", ""}, "h", "x-y_v1-2_2026-11-25_21-03.tar.gz"},
	}
	for _, c := range cases {
		if got := backupArchiveName(c.id, c.host, at); got != c.want {
			t.Errorf("got %q, want %q", got, c.want)
		}
	}
}
