package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/config"
)

const defaultStreamPort = 8001

var m3uFileName = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

func cleanChannelName(name string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(name) {
		if r >= ' ' && r != 0x7f {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if len(out) > 120 {
		out = out[:120]
	}
	return out
}

func streamM3U(profile config.Profile, ref, name string) string {
	host := strings.TrimSpace(profile.Host)
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && strings.Contains(ip.String(), ":") {
		host = "[" + ip.String() + "]"
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXTVLCOPT:http-reconnect=true\n")
	if name != "" {
		b.WriteString("#EXTINF:-1," + name + "\n")
	}
	b.WriteString("http://" + host + ":" + strconv.Itoa(defaultStreamPort) + "/" + ref + "\n")
	return b.String()
}

func (s *Server) deviceStream(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if !serviceRefPattern.MatchString(ref) || strings.ContainsAny(ref, " \"'<>\\") {
		writeError(w, http.StatusBadRequest, "not a channel reference")
		return
	}
	name := cleanChannelName(r.URL.Query().Get("name"))
	playlist := ""
	web := newDeviceWeb(profile, 5*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	query := "/web/stream.m3u?ref=" + url.QueryEscape(ref)
	if name != "" {
		query += "&name=" + url.QueryEscape(name)
	}
	if status, body, err := web.getBounded(ctx, query, 8192); err == nil && status == http.StatusOK {
		if text := strings.TrimSpace(string(body)); strings.HasPrefix(text, "#EXTM3U") && strings.Contains(text, "http") {
			playlist = text + "\n"
		}
	}
	if playlist == "" {
		playlist = streamM3U(profile, ref, name)
	}
	file := strings.Trim(m3uFileName.ReplaceAllString(name, "_"), "._-")
	if file == "" {
		file = "channel"
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Header().Set("Content-Disposition", `attachment; filename="`+file+`.m3u"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(playlist))
}
