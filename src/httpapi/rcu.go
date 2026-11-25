package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/terminal"
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
	Key      string `json:"key"`
	Long     bool   `json:"long,omitempty"`
	Hardware bool   `json:"hardware,omitempty"`
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
	keyName := strings.TrimSpace(input.Key)
	code, known := remoteKeys[strings.ToLower(keyName)]
	if !known {
		code, known = linuxKeys[strings.ToUpper(keyName)]
	}
	if !known {
		writeError(w, http.StatusBadRequest, "unknown remote key")
		return
	}
	if input.Hardware {
		hctx, hcancel := context.WithTimeout(r.Context(), 12*time.Second)
		err := injectHardwareKey(hctx, profile, code, input.Long)
		hcancel()
		if err == nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": true, "via": "input-device"})
			return
		}
		devLog.Warn("hardware key injection failed, using the web interface", "host", profile.Host, "error", err)
	}
	web := newDeviceWeb(profile, 8*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, keyPriorityKey{}, true)

	suffix := "?command=" + strconv.Itoa(code)
	if input.Long {
		suffix += "&type=long"
	}
	status, body, err := web.pressRemote(ctx, "/api/remotecontrol"+suffix)
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
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()

	query := url.Values{}
	query.Set("text", strings.ReplaceAll(text, "%", "%25"))
	status, body, err := web.pressRemote(ctx, "/api/remotecontrol?"+query.Encode())
	if err == nil && remoteRefused(status, body) && len(text) <= 64 {
		status, body, err = web.typeAscii(ctx, text)
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
	status, body, err := web.pressRemote(ctx, "/api/message?"+query.Encode())
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
	case webUnsupported(status):
		writeWebUnsupported(w)
		return true
	case status != http.StatusOK:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("The web interface answered HTTP %d.", status), "code": "web_error"})
		return true
	}

	if state, text, ok := xmlResult(body); ok && !state {
		if text == "" {
			text = "The receiver did not accept the request."
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": text, "code": "web_error"})
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
	api := strings.HasPrefix(pathAndQuery, "/api/")
	status, body, err := d.viaLegacy(ctx, pathAndQuery, func() (int, []byte, error) { return d.pressRaw(ctx, pathAndQuery) })
	if err != nil || !api {
		return status, body, err
	}
	switch status {
	case http.StatusMethodNotAllowed, http.StatusNotImplemented:
		status = http.StatusNotFound
	case http.StatusOK:
		if t := bytes.TrimSpace(body); len(t) > 0 && !looksJSON(t) {
			status = http.StatusNotFound
		}
	}
	return status, body, nil
}

func (d deviceWeb) pressFallback(ctx context.Context, apiPath string) (int, []byte, error) {
	if d.kind == "dreamos" {
		return d.press(ctx, "/web/"+strings.TrimPrefix(apiPath, "/api/"))
	}
	return d.press(ctx, apiPath)
}

func webUnsupported(status int) bool {
	return status == http.StatusNotFound || status == http.StatusMethodNotAllowed || status == http.StatusNotImplemented
}

func writeWebUnsupported(w http.ResponseWriter) {
	writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "This receiver's web interface does not offer this action.", "code": "web_unsupported"})
}

var (
	xmlStatePattern = regexp.MustCompile(`(?is)<e2state>\s*([^<\s]*)\s*</e2state>`)
	xmlTextPattern  = regexp.MustCompile(`(?is)<e2statetext>(.*?)</e2statetext>`)
)

func xmlResult(body []byte) (state bool, text string, ok bool) {
	m := xmlStatePattern.FindSubmatch(body)
	if m == nil {
		return false, "", false
	}
	if t := xmlTextPattern.FindSubmatch(body); t != nil {
		text = strings.TrimSpace(string(t[1]))
	}
	return xmlBool(string(m[1])), text, true
}

func (d deviceWeb) pressRaw(ctx context.Context, pathAndQuery string) (int, []byte, error) {
	resp, err := d.get(ctx, pathAndQuery)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, body, nil
}

func (d deviceWeb) pressRemote(ctx context.Context, apiPath string) (int, []byte, error) {
	status, body, err := d.pressFallback(ctx, apiPath)
	if err != nil || !webUnsupported(status) {
		return status, body, err
	}
	alt := apiPath
	if d.kind != "dreamos" {
		alt = "/web/" + strings.TrimPrefix(apiPath, "/api/")
	}
	altStatus, altBody, altErr := d.pressRaw(ctx, alt)
	if altErr != nil || altStatus != http.StatusOK {
		return status, body, err
	}
	if _, _, isXML := xmlResult(altBody); !isXML && !looksJSON(bytes.TrimSpace(altBody)) {
		return status, body, err // a web page, not a remote answer
	}
	return altStatus, altBody, nil
}

func remoteRefused(status int, body []byte) bool {
	if webUnsupported(status) {
		return true
	}
	if status != http.StatusOK {
		return false
	}
	if state, _, ok := xmlResult(body); ok && !state {
		return true
	}
	var reply struct {
		Result *bool `json:"result"`
	}
	return json.Unmarshal(body, &reply) == nil && reply.Result != nil && !*reply.Result
}

func (d deviceWeb) typeAscii(ctx context.Context, text string) (int, []byte, error) {
	status, body := http.StatusOK, []byte(nil)
	for i := 0; i < len(text); i++ {
		var err error
		status, body, err = d.pressRemote(ctx, "/api/remotecontrol?command="+strconv.Itoa(int(text[i]))+"&type=ascii")
		if err != nil || status != http.StatusOK || remoteRefused(status, body) {
			return status, body, err
		}
	}
	return status, body, nil
}

var dreamboxModel = regexp.MustCompile(`\b(dm\s?-?\d{3,4}|dream\s?(box|one|two))`)

func remoteFamily(kind, brand, model, image string) string {
	text := strings.ToLower(strings.Join([]string{brand, model, image}, " "))
	switch {
	case kind == "dreamos" || dreamboxModel.MatchString(text):
		return "dreambox"
	case strings.Contains(text, "vuplus") || strings.Contains(text, "vu+") || strings.HasPrefix(strings.TrimSpace(strings.ToLower(brand)), "vu"):
		return "vu"
	case strings.Contains(text, "gigablue") || strings.HasPrefix(strings.TrimSpace(strings.ToLower(brand)), "gb"):
		return "gigablue"
	}
	return "standard"
}

func (s *Server) deviceRemoteProfile(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 6*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	var brand, model, image, build, boxtype string
	if status, about, err := getJSONMap(ctx, web, "/api/about"); err == nil && status == http.StatusOK {
		info := asMap(about["info"])
		brand, model = jsonText(info["brand"]), jsonText(info["model"])
		build, boxtype = jsonText(info["machinebuild"]), jsonText(info["boxtype"])
		image = jsonText(info["friendlyimagedistro"])
		if image == "" {
			image = jsonText(info["imagedistro"])
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{
		"kind": web.kind, "family": remoteFamily(web.kind, brand, model, image),
		"brand": brand, "model": model, "image": image, "machinebuild": build, "boxtype": boxtype,
	})
}

func hardwareKeyCommand(code int, long bool) string {
	py := `import os,struct,time,re;` +
		`b=open('/proc/bus/input/devices').read().split('\n\n');` +
		`d=[re.search(r'event[0-9]+',x).group() for x in b if re.search(r'Name=.*(remote|rc)',x,re.I) and re.search(r'event[0-9]+',x)];` +
		`f=os.open('/dev/input/'+(d[0] if d else 'event0'),os.O_WRONLY);` +
		`w=lambda t,c,v:os.write(f,struct.pack('llHHi',int(time.time()),0,t,c,v));` +
		`w(1,` + strconv.Itoa(code) + `,1);w(0,0,0);`
	if long {
		py += `[(time.sleep(0.12),w(1,` + strconv.Itoa(code) + `,2),w(0,0,0)) for i in range(9)];`
	} else {
		py += `time.sleep(0.08);`
	}
	py += `w(1,` + strconv.Itoa(code) + `,0);w(0,0,0)`
	return `PY=$(command -v python3 || command -v python) && [ -n "$PY" ] && $PY -c "` + py + `"`
}

func injectHardwareKey(ctx context.Context, profile config.Profile, code int, long bool) error {
	out, status, err := terminal.RunCommand(ctx, profile.Host, profile.EffectiveTelnetPort(), telnetUser(profile), profile.Password, hardwareKeyCommand(code, long), 64<<10)
	if err != nil {
		return err
	}
	if status != 0 {
		return errors.New("the receiver could not write to its remote-control device: " + strings.TrimSpace(out))
	}
	return nil
}
