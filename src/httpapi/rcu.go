package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

var remoteKeys = map[string]int{
	"power": 116,
	"1":     2, "2": 3, "3": 4, "4": 5, "5": 6, "6": 7, "7": 8, "8": 9, "9": 10, "0": 11,
	"prev": 412, "next": 407,
	"red": 398, "green": 399, "yellow": 400, "blue": 401,
	"menu": 139, "exit": 174, "info": 358, "epg": 365,
	"up": 103, "down": 108, "left": 105, "right": 106, "ok": 352,
	"volup": 115, "voldown": 114, "mute": 113, "chup": 402, "chdown": 403,
	"history": 405,
	"rewind":  168, "play": 207, "pause": 119, "forward": 208,
	"skipback": 165, "record": 167, "stop": 128, "skipnext": 163,
	"text": 388, "audio": 392, "subtitle": 370, "video": 393,
	"pvr": 366, "tv": 377, "radio": 385, "help": 138,
	"setup": 141, "portal": 172, "sleep": 142, "timer": 362,
	"f1": 59, "f2": 60, "fav": 364, "pip": 378,
	"back": 158, "opt": 349, "context": 438, "aspect": 375,
}

type remoteKeyRequest struct {
	Key  string `json:"key"`
	Long bool   `json:"long,omitempty"`
}

func (s *Server) deviceRemoteKey(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.app.Profiles.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "device profile not found")
		return
	}
	if !profile.Enabled {
		writeError(w, http.StatusConflict, "device profile is disabled")
		return
	}
	var input remoteKeyRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	code, known := remoteKeys[strings.ToLower(strings.TrimSpace(input.Key))]
	if !known {
		writeError(w, http.StatusBadRequest, "unknown remote key")
		return
	}
	web := newDeviceWeb(profile, 8*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	suffix := "?command=" + strconv.Itoa(code)
	if input.Long {
		suffix += "&type=long"
	}
	status, body, err := web.press(ctx, "/api/remotecontrol"+suffix)
	if err == nil && status == http.StatusNotFound {

		status, body, err = web.press(ctx, "/web/remotecontrol"+suffix)
	}
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type deviceTextRequest struct {
	Text string `json:"text"`
}

func (s *Server) deviceRemoteText(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var input deviceTextRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	text := input.Text
	if text == "" || len(text) > 200 {
		writeError(w, http.StatusUnprocessableEntity, "type 1 to 200 characters")
		return
	}
	for i := 0; i < len(text); i++ {
		if text[i] < 0x20 || text[i] > 0x7e {
			writeError(w, http.StatusUnprocessableEntity, "only plain letters, digits and symbols (no accents, no line breaks) can be typed on the receiver")
			return
		}
	}
	web := newDeviceWeb(profile, 8*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()

	query := url.Values{}
	query.Set("text", strings.ReplaceAll(text, "%", "%25"))
	status, body, err := web.press(ctx, "/api/remotecontrol?"+query.Encode())
	if err == nil && status == http.StatusNotFound {
		status, body, err = web.press(ctx, "/web/remotecontrol?"+query.Encode())
	}
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type deviceMessageRequest struct {
	Text    string `json:"text"`
	Seconds int    `json:"seconds,omitempty"`
}

func (s *Server) deviceMessage(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.app.Profiles.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "device profile not found")
		return
	}
	if !profile.Enabled {
		writeError(w, http.StatusConflict, "device profile is disabled")
		return
	}
	var input deviceMessageRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	text := strings.TrimSpace(input.Text)
	if text == "" || len(text) > 300 || strings.ContainsAny(text, "\r\n\x00") {
		writeError(w, http.StatusUnprocessableEntity, "the message must be one line of 1 to 300 characters")
		return
	}
	seconds := input.Seconds
	if seconds < 1 || seconds > 60 {
		seconds = 10
	}
	web := newDeviceWeb(profile, 8*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	query := url.Values{}
	query.Set("text", text)
	query.Set("type", "1")
	query.Set("timeout", strconv.Itoa(seconds))
	status, body, err := web.press(ctx, "/api/message?"+query.Encode())
	if err == nil && status == http.StatusNotFound {
		status, body, err = web.press(ctx, "/web/message?"+query.Encode())
	}
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func writeWebFailure(w http.ResponseWriter, web deviceWeb, status int, body []byte, err error) bool {
	switch {
	case err != nil:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": web.unreachable(err), "code": "web_unreachable"})
		return true
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver's web interface rejected the saved user name or password.", "code": "web_auth"})
		return true
	case status != http.StatusOK:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("The web interface answered HTTP %d.", status), "code": "web_error"})
		return true
	}

	var reply struct {
		Result  *bool  `json:"result"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &reply) == nil && reply.Result != nil && !*reply.Result {
		message := strings.TrimSpace(reply.Message)
		if message == "" {
			message = "The receiver did not accept the request."
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": message, "code": "web_error"})
		return true
	}
	return false
}

func (d deviceWeb) press(ctx context.Context, pathAndQuery string) (int, []byte, error) {
	resp, err := d.get(ctx, pathAndQuery)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, body, nil
}
