package httpapi

import (
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
)

// Channel list, zapping, "now playing" and picons for the Screen page. Like the
// live picture and the remote keys, everything is relayed to the web interface
// (OpenWebif) of the saved device: the browser never supplies an address, only a
// service reference, which is validated before it is put in a request.

const (
	maxChannelListBytes = 24 << 20
	maxPiconBytes       = 512 << 10
)

// serviceRefPattern accepts an Enigma2 service reference: ten colon separated
// fields (type, flags, service id, …) followed by an optional path/URL and name.
var serviceRefPattern = regexp.MustCompile(`^[0-9]+:[0-9]+(:[0-9A-Fa-f]+){8}:[^\x00-\x1f\x7f]{0,900}$`)

// piconRefPattern matches the first ten fields that name a picon file.
var piconRefPattern = regexp.MustCompile(`^([0-9]+(?::[0-9A-Fa-f]+){9}):`)

// piconSlots limits how many picons are fetched from the receiver at once, so
// a long channel list cannot flood a small box.
var piconSlots = make(chan struct{}, 4)

func (s *Server) enabledProfile(w http.ResponseWriter, r *http.Request) (config.Profile, bool) {
	profile, ok := s.app.Profiles.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "device profile not found")
		return profile, false
	}
	if !profile.Enabled {
		writeError(w, http.StatusConflict, "device profile is disabled")
		return profile, false
	}
	return profile, true
}

// getBounded performs a GET on the device and returns status and a body of at most limit bytes.
func (d deviceWeb) getBounded(ctx context.Context, pathAndQuery string, limit int64) (int, []byte, error) {
	resp, err := d.get(ctx, pathAndQuery)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return resp.StatusCode, nil, err
	}
	if int64(len(body)) > limit {
		return resp.StatusCode, nil, errors.New("the receiver's answer is too large")
	}
	return resp.StatusCode, body, nil
}

// writeWebStatus answers for a bad HTTP status or transport error and reports
// whether it wrote an error. Unlike writeWebFailure it does not inspect the body.
func writeWebStatus(w http.ResponseWriter, web deviceWeb, status int, err error) bool {
	switch {
	case err != nil:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": web.unreachable(err), "code": "web_unreachable"})
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver's web interface rejected the saved user name or password.", "code": "web_auth"})
	case status != http.StatusOK:
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("The web interface answered HTTP %d.", status), "code": "web_error"})
	default:
		return false
	}
	return true
}

type channelEntry struct {
	Ref    string `json:"ref"`
	Name   string `json:"name"`
	Number int    `json:"number,omitempty"`
	Marker bool   `json:"marker,omitempty"`
	Picon  bool   `json:"picon,omitempty"`
}

type channelBouquet struct {
	Ref      string         `json:"ref"`
	Name     string         `json:"name"`
	Channels []channelEntry `json:"channels"`
}

// serviceFlags returns the second field of a service reference (64 = marker).
func serviceFlags(ref string) int {
	parts := strings.SplitN(ref, ":", 3)
	if len(parts) < 2 {
		return 0
	}
	n, _ := strconv.Atoi(parts[1])
	return n
}

// piconFile returns the picon file name for a reference, or "" when the
// reference has no picon by number (IPTV streams are named by channel).
func piconFile(ref string) string {
	m := piconRefPattern.FindStringSubmatch(strings.ToUpper(ref))
	if m == nil {
		return ""
	}
	return strings.ReplaceAll(m[1], ":", "_") + ".png"
}

// deviceChannels returns the bouquets and channels of the receiver (TV or radio).
func (s *Server) deviceChannels(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	path := "/api/getallservices"
	switch r.URL.Query().Get("type") {
	case "", "tv":
	case "radio":
		path += "?type=radio" // OpenWebif only looks for the presence of "type"
	default:
		writeError(w, http.StatusBadRequest, "type must be tv or radio")
		return
	}
	web := newDeviceWeb(profile, 25*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, path, maxChannelListBytes)
	if writeWebStatus(w, web, status, err) {
		return
	}
	var raw struct {
		Services []struct {
			Ref  string `json:"servicereference"`
			Name string `json:"servicename"`
			Sub  []struct {
				Ref  string          `json:"servicereference"`
				Name string          `json:"servicename"`
				Pos  json.RawMessage `json:"pos"`
			} `json:"subservices"`
		} `json:"services"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return a channel list (is OpenWebif installed and up to date?).", "code": "web_error"})
		return
	}
	out := make([]channelBouquet, 0, len(raw.Services))
	for _, b := range raw.Services {
		bouquet := channelBouquet{Ref: b.Ref, Name: b.Name, Channels: make([]channelEntry, 0, len(b.Sub))}
		for _, c := range b.Sub {
			if !serviceRefPattern.MatchString(c.Ref) {
				continue
			}
			entry := channelEntry{Ref: c.Ref, Name: strings.TrimSpace(c.Name)}
			if n, err := strconv.Atoi(strings.Trim(string(c.Pos), `"`)); err == nil {
				entry.Number = n
			}
			if serviceFlags(c.Ref)&64 != 0 {
				entry.Marker = true
			} else {
				entry.Picon = piconFile(c.Ref) != ""
			}
			bouquet.Channels = append(bouquet.Channels, entry)
		}
		out = append(out, bouquet)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"bouquets": out})
}

type zapRequest struct {
	Ref string `json:"ref"`
}

// deviceZap switches the receiver to a channel (OpenWebif "zap").
func (s *Server) deviceZap(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var input zapRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ref := strings.TrimSpace(input.Ref)
	if !serviceRefPattern.MatchString(ref) || serviceFlags(ref)&64 != 0 {
		writeError(w, http.StatusBadRequest, "not a channel reference")
		return
	}
	web := newDeviceWeb(profile, 10*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, body, err := web.press(ctx, "/api/zap?sRef="+url.QueryEscape(ref))
	if err == nil && status == http.StatusNotFound {
		status, body, err = web.press(ctx, "/web/zap?sRef="+url.QueryEscape(ref))
	}
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type currentEvent struct {
	Title     string `json:"title"`
	Begin     int64  `json:"begin,omitempty"`
	Duration  int64  `json:"duration,omitempty"`
	Remaining int64  `json:"remaining,omitempty"`
}

// deviceCurrent returns the channel the receiver is tuned to, with the running
// and the next programme (OpenWebif "getcurrent").
func (s *Server) deviceCurrent(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 6*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, "/api/getcurrent", 1<<20)
	if writeWebStatus(w, web, status, err) {
		return
	}
	type rawEvent struct {
		Title     string          `json:"title"`
		Begin     json.RawMessage `json:"begin_timestamp"`
		Duration  json.RawMessage `json:"duration_sec"`
		Remaining json.RawMessage `json:"remaining"`
	}
	var raw struct {
		Info struct {
			Ref  string `json:"ref"`
			Name string `json:"name"`
		} `json:"info"`
		Now  rawEvent `json:"now"`
		Next rawEvent `json:"next"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not describe the current channel.", "code": "web_error"})
		return
	}
	num := func(m json.RawMessage) int64 {
		v, _ := strconv.ParseInt(strings.Trim(string(m), `" `), 10, 64)
		return v
	}
	event := func(e rawEvent) *currentEvent {
		title := strings.TrimSpace(e.Title)
		if title == "" {
			return nil
		}
		return &currentEvent{Title: title, Begin: num(e.Begin), Duration: num(e.Duration), Remaining: num(e.Remaining)}
	}
	ref := raw.Info.Ref
	if !serviceRefPattern.MatchString(ref) {
		ref = ""
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"ref": ref, "name": strings.TrimSpace(raw.Info.Name), "now": event(raw.Now), "next": event(raw.Next), "standby": deviceStandby(ctx, web)})
}

// devicePicon relays a channel logo from the receiver. The file names are built
// by piconCandidates from a validated service reference and the channel name,
// the same fallbacks OpenWebif uses, and the first real PNG wins.
func (s *Server) devicePicon(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	ref := r.URL.Query().Get("ref")
	name := r.URL.Query().Get("name")
	candidates := piconCandidates(ref, name)
	if len(candidates) == 0 {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	key := profile.ID + "|" + profile.Host + "|" + ref + "|" + name
	if file, known := piconCache.get(key); known {
		if file == "" {
			w.Header().Set("Cache-Control", "private, max-age=300")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		candidates = []string{file}
	}
	select {
	case piconSlots <- struct{}{}:
		defer func() { <-piconSlots }()
	case <-r.Context().Done():
		return
	}
	web := newDeviceWeb(profile, 6*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	unreachable := false
	for _, file := range candidates {
		status, body, err := web.getBounded(ctx, "/picon/"+file, maxPiconBytes)
		if err != nil {
			unreachable = true // browser left or receiver down: do not remember a miss
			break
		}
		if status != http.StatusOK || len(body) < 8 || string(body[1:4]) != "PNG" {
			continue
		}
		piconCache.put(key, file)
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Cache-Control", "private, max-age=86400")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		_, _ = w.Write(body)
		return
	}
	if !unreachable && ctx.Err() == nil {
		piconCache.put(key, "")
	}
	w.Header().Set("Cache-Control", "private, max-age=300")
	w.WriteHeader(http.StatusNoContent) // no console noise for channels without a logo
}

type epgNowEntry struct {
	Title    string `json:"title"`
	Begin    int64  `json:"begin"`
	Duration int64  `json:"duration"`
}

// deviceEPGNow returns the programme running now on every channel of one
// bouquet (OpenWebif "epgnow"), so the channel list can show what is on, like
// the OpenWebif bouquet view does. The bouquet reference is validated first.
func (s *Server) deviceEPGNow(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	bouquet := strings.TrimSpace(r.URL.Query().Get("bouquet"))
	if !serviceRefPattern.MatchString(bouquet) {
		writeError(w, http.StatusBadRequest, "not a bouquet reference")
		return
	}
	web := newDeviceWeb(profile, 20*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, "/api/epgnow?bRef="+url.QueryEscape(bouquet), maxChannelListBytes)
	if writeWebStatus(w, web, status, err) {
		return
	}
	var raw struct {
		Events []struct {
			Ref      string          `json:"sref"`
			Title    string          `json:"title"`
			Begin    json.RawMessage `json:"begin_timestamp"`
			Duration json.RawMessage `json:"duration_sec"`
			Now      json.RawMessage `json:"now_timestamp"`
		} `json:"events"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return the programme guide.", "code": "web_error"})
		return
	}
	num := func(m json.RawMessage) int64 {
		v, _ := strconv.ParseInt(strings.Trim(string(m), `" `), 10, 64)
		return v
	}
	events := make(map[string]epgNowEntry, len(raw.Events))
	now := time.Now().Unix()
	for _, e := range raw.Events {
		title := strings.TrimSpace(e.Title)
		if title == "" || !serviceRefPattern.MatchString(e.Ref) {
			continue
		}
		events[e.Ref] = epgNowEntry{Title: title, Begin: num(e.Begin), Duration: num(e.Duration)}
		if n := num(e.Now); n > 0 {
			now = n // the receiver's own clock, so elapsed time is right even if clocks differ
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "events": events})
}

// powerActions maps the names the browser may send to OpenWebif "powerstate"
// values (0 toggle, 1 deep standby, 2 reboot, 3 restart GUI, 4 wake up, 5 standby).
var powerActions = map[string]string{
	"toggle": "0", "deep": "1", "reboot": "2", "restart_gui": "3", "wakeup": "4", "standby": "5",
}

type powerRequest struct {
	Action string `json:"action"`
}

// devicePower runs a power action on the receiver, or reloads its channel lists
// (action "reload"). Only names from a fixed list are accepted.
func (s *Server) devicePower(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var input powerRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	action := strings.ToLower(strings.TrimSpace(input.Action))
	path := ""
	if action == "reload" {
		path = "/api/servicelistreload?mode=0"
	} else if state, known := powerActions[action]; known {
		path = "/api/powerstate?newstate=" + state
	} else {
		writeError(w, http.StatusBadRequest, "unknown power action")
		return
	}
	web := newDeviceWeb(profile, 10*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, body, err := web.press(ctx, path)
	// A receiver that is rebooting may drop the connection before it answers.
	if err != nil && (action == "deep" || action == "reboot" || action == "restart_gui") {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	if action == "reload" {
		piconCache.forget()
	}
	// OpenWebif reports the standby state from before the action ran (it reads the
	// state before opening the Standby screen), so callers must poll for the new one.
	var reply struct {
		InStandby *bool `json:"instandby"`
	}
	resp := map[string]any{"ok": true}
	if json.Unmarshal(body, &reply) == nil && reply.InStandby != nil {
		resp["standby"] = *reply.InStandby
	}
	writeJSON(w, http.StatusOK, resp)
}

// deviceStandby reports whether the receiver is in standby (nil when unknown).
func deviceStandby(ctx context.Context, web deviceWeb) *bool {
	status, body, err := web.press(ctx, "/api/powerstate")
	if err != nil || status != http.StatusOK {
		return nil
	}
	var reply struct {
		InStandby *bool `json:"instandby"`
	}
	if json.Unmarshal(body, &reply) != nil {
		return nil
	}
	return reply.InStandby
}
