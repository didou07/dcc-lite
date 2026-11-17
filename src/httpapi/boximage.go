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

const dreamboxSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 320 120"><defs><linearGradient id="g" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="#3a3d42"/><stop offset="1" stop-color="#1b1d20"/></linearGradient><linearGradient id="d" x1="0" y1="0" x2="1" y2="1"><stop offset="0" stop-color="#ff5a36"/><stop offset=".5" stop-color="#b04fd6"/><stop offset="1" stop-color="#2f7bff"/></linearGradient></defs><path d="M14 30h292l-6 74H20z" fill="url(#g)" stroke="#555a61"/><rect x="14" y="30" width="292" height="10" rx="3" fill="#4a4e54"/><path d="M20 58 130 40l28 64H20z" fill="#2a2c30"/><rect x="190" y="52" width="76" height="40" rx="3" fill="#0b0b0d" stroke="#555a61"/><rect x="196" y="57" width="64" height="30" rx="2" fill="url(#d)"/><text x="30" y="84" font-family="sans-serif" font-size="11" fill="#b8bcc2" letter-spacing="2">dreambox</text><rect x="168" y="68" width="14" height="4" rx="2" fill="#c0392b"/></svg>`

func isDreambox(about map[string]any) bool {
	info := asMap(about["info"])
	for _, key := range []string{"machinebuild", "boxtype", "model"} {
		v := strings.ToLower(strings.TrimSpace(jsonText(info[key])))
		if len(v) >= 3 && strings.HasPrefix(v, "dm") && v[2] >= '0' && v[2] <= '9' {
			return true
		}
	}
	return false
}

func writeDreamboxSVG(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("Cache-Control", "private, max-age=86400")
	_, _ = w.Write([]byte(dreamboxSVG))
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
		if path == "svg:dreambox" {
			writeDreamboxSVG(w)
			return
		}
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
	dream := false
	if candidates == nil {
		status, about, err := getJSONMap(ctx, web, "/api/about")
		if err != nil || status != http.StatusOK {
			w.Header().Set("Cache-Control", "no-store")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		candidates = boxImageCandidates(about)
		dream = isDreambox(about)
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
		if dream {
			piconCache.put(key, "svg:dreambox")
			writeDreamboxSVG(w)
			return
		}
		piconCache.put(key, "")
	}
	w.Header().Set("Cache-Control", "private, max-age=600")
	w.WriteHeader(http.StatusNoContent)
}
