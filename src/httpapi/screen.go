package httpapi

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

const maxScreenBytes = 6 << 20

// screenWidths are the picture widths the receiver is asked for (OpenWebif "r").
var screenWidths = map[int]bool{320: true, 480: true, 640: true, 720: true, 960: true, 1280: true, 1920: true}

// deviceScreen returns one JPEG of what the receiver shows on the TV (video,
// menus, or both) by asking the receiver's own web interface. The browser calls
// it about once a second, which gives a live view without any extra software on
// the receiver. The request goes only to the host of the saved device.
func (s *Server) deviceScreen(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.app.Profiles.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "device profile not found")
		return
	}
	if !profile.Enabled {
		writeError(w, http.StatusConflict, "device profile is disabled")
		return
	}
	mode := r.URL.Query().Get("mode")
	switch mode {
	case "all", "video", "osd":
	case "":
		mode = "all"
	default:
		writeError(w, http.StatusBadRequest, "mode must be all, video or osd")
		return
	}
	width := 720
	if raw := r.URL.Query().Get("width"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || !screenWidths[n] {
			writeError(w, http.StatusBadRequest, "width must be 320, 480, 640, 720, 960, 1280 or 1920")
			return
		}
		width = n
	}
	web := newDeviceWeb(profile, 10*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	resp, err := web.get(ctx, fmt.Sprintf("/grab?format=jpg&mode=%s&r=%d", mode, width))
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": web.unreachable(err), "code": "web_unreachable"})
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver's web interface rejected the saved user name or password.", "code": "web_auth"})
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("The web interface answered HTTP %d.", resp.StatusCode), "code": "web_error"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxScreenBytes+1))
	if err != nil || len(body) > maxScreenBytes {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver sent an unusable picture.", "code": "web_error"})
		return
	}
	// Never relay anything that is not a JPEG, whatever the device answered.
	if len(body) < 3 || body[0] != 0xFF || body[1] != 0xD8 || body[2] != 0xFF {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return a picture (is the grab feature available on this image?).", "code": "web_error"})
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	_, _ = w.Write(body)
}
