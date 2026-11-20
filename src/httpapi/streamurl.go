package httpapi

import (
	"context"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/config"
)

func streamHostPort(profile config.Profile, port int) string {
	return net.JoinHostPort(strings.Trim(strings.TrimSpace(profile.Host), "[]"), strconv.Itoa(port))
}

func liveUpstreamURL(ctx context.Context, profile config.Profile, ref string) string {
	fallback := "http://" + streamHostPort(profile, defaultStreamPort) + "/" + ref
	web := newDeviceWeb(profile, 5*time.Second)
	status, body, err := web.getBounded(ctx, "/web/stream.m3u?ref="+url.QueryEscape(ref), 8192)
	if err != nil || status != http.StatusOK {
		return fallback
	}
	want := strings.Trim(strings.TrimSpace(profile.Host), "[]")
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "http://") && !strings.HasPrefix(line, "https://") {
			continue
		}
		u, err := url.Parse(line)
		if err != nil || !strings.EqualFold(u.Hostname(), want) {
			continue
		}
		return line
	}
	return fallback
}

func ValidServiceRef(ref string) bool {
	return serviceRefPattern.MatchString(ref) && !strings.ContainsAny(ref, " \"'<>\\")
}

func PlaybackURL(ctx context.Context, profile config.Profile, ref string) (string, bool) {
	ref = strings.TrimSpace(ref)
	if !ValidServiceRef(ref) {
		return "", false
	}
	u, err := url.Parse(liveUpstreamURL(ctx, profile, ref))
	if err != nil || u.Host == "" {
		return "", false
	}
	if u.User == nil && profile.Password != "" {
		user := strings.TrimSpace(profile.Username)
		if user == "" {
			user = config.DefaultUsername
		}
		u.User = url.UserPassword(user, profile.Password)
	}
	return u.String(), true
}
