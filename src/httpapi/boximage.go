package httpapi

import (
	"context"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const maxBoxImageBytes = 1 << 20

var boxNameSafe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

func boxImageCandidates(about map[string]any) []string {
	info := asMap(about["info"])
	var names []string
	add := func(v string) {
		v = strings.TrimSpace(v)
		if v == "" {
			return
		}
		for _, n := range []string{v, strings.ToLower(v), strings.NewReplacer(" ", "", "-", "", "_", "", "+", "plus", "²", "2").Replace(strings.ToLower(v))} {
			if !boxNameSafe.MatchString(n) {
				continue
			}
			dup := false
			for _, have := range names {
				if have == n {
					dup = true
					break
				}
			}
			if !dup {
				names = append(names, n)
			}
		}
	}
	for _, key := range []string{"machinebuild", "boxtype", "model"} {
		add(jsonText(info[key]))
	}
	add(joinWords(jsonText(info["brand"]), jsonText(info["model"])))
	var out []string
	for _, n := range names {
		for _, dir := range []string{"/images/boxes/", "/public/images/boxes/"} {
			out = append(out, dir+n+".png", dir+n+".jpg")
		}
	}
	if len(out) > 24 {
		out = out[:24]
	}
	return out
}

func isBoxImage(body []byte) string {
	if len(body) < 16 {
		return ""
	}
	switch t := http.DetectContentType(body); t {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return t
	}
	return ""
}

func (s *Server) deviceBoxImage(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	key := "box|" + profile.ID + "|" + profile.Host
	var candidates []string
	if path, known := piconCache.get(key); known {
		if path == "" {
			w.Header().Set("Cache-Control", "private, max-age=600")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		candidates = []string{path}
	}
	select {
	case piconSlots <- struct{}{}:
		defer func() { <-piconSlots }()
	case <-r.Context().Done():
		return
	}
	web := newDeviceWeb(profile, 6*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	if candidates == nil {
		status, about, err := getJSONMap(ctx, web, "/api/about")
		if err != nil || status != http.StatusOK {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		candidates = boxImageCandidates(about)
	}
	unreachable := false
	for _, path := range candidates {
		status, body, err := web.getBounded(ctx, path, maxBoxImageBytes)
		if err != nil {
			unreachable = true
			break
		}
		ctype := ""
		if status == http.StatusOK {
			ctype = isBoxImage(body)
		}
		if ctype == "" {
			continue
		}
		piconCache.put(key, path)
		w.Header().Set("Content-Type", ctype)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
		return
	}
	if !unreachable && ctx.Err() == nil {
		piconCache.put(key, "")
	}
	w.Header().Set("Cache-Control", "private, max-age=600")
	w.WriteHeader(http.StatusNoContent)
}
