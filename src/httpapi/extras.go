package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func rawString(m json.RawMessage) string {
	var s string
	if json.Unmarshal(m, &s) != nil {
		return ""
	}
	return s
}

func rawBool(m json.RawMessage) bool {
	switch strings.ToLower(strings.Trim(string(m), `" `)) {
	case "true", "1":
		return true
	}
	return false
}

func timerDir(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || strings.EqualFold(dir, "none") || !moviePathOK(dir) {
		return ""
	}
	return dir
}

func channelRefOK(ref string) bool {
	return serviceRefPattern.MatchString(ref) && serviceFlags(ref)&64 == 0 && !strings.ContainsAny(ref, " \"'<>\\")
}

func boolFlag(on bool) string {
	if on {
		return "1"
	}
	return "0"
}

type timerChangeRequest struct {
	OldRef      string `json:"old_ref"`
	OldBegin    int64  `json:"old_begin"`
	OldEnd      int64  `json:"old_end"`
	Ref         string `json:"ref"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Begin       int64  `json:"begin"`
	End         int64  `json:"end"`
	JustPlay    bool   `json:"justplay"`
	Disabled    bool   `json:"disabled"`
	AfterEvent  int    `json:"afterevent"`
	Repeated    int    `json:"repeated"`
	Dir         string `json:"dir"`
	Tags        string `json:"tags"`
}

func (s *Server) deviceTimerChange(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var in timerChangeRequest
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	in.Ref, in.OldRef = strings.TrimSpace(in.Ref), strings.TrimSpace(in.OldRef)
	if !channelRefOK(in.Ref) || !channelRefOK(in.OldRef) {
		writeError(w, http.StatusBadRequest, "not a channel reference")
		return
	}
	if in.OldBegin <= 0 || in.OldEnd <= in.OldBegin {
		writeError(w, http.StatusBadRequest, "not a timer")
		return
	}
	name := cleanText(in.Name, 200)
	if name == "" {
		writeError(w, http.StatusBadRequest, "the timer needs a name")
		return
	}
	if in.Begin <= 0 || in.End <= in.Begin || in.End-in.Begin > maxTimerSpan {
		writeError(w, http.StatusBadRequest, "the timer needs a start before its end, at most 48 hours long")
		return
	}
	if in.AfterEvent < 0 || in.AfterEvent > 3 {
		in.AfterEvent = 3
	}
	if in.Repeated < 0 || in.Repeated > 127 {
		writeError(w, http.StatusBadRequest, "the repeat days are not valid")
		return
	}
	path := "/api/timerchange?sRef=" + url.QueryEscape(in.Ref) +
		"&begin=" + strconv.FormatInt(in.Begin, 10) + "&end=" + strconv.FormatInt(in.End, 10) +
		"&name=" + url.QueryEscape(name) + "&description=" + url.QueryEscape(cleanText(in.Description, 300)) +
		"&channelOld=" + url.QueryEscape(in.OldRef) +
		"&beginOld=" + strconv.FormatInt(in.OldBegin, 10) + "&endOld=" + strconv.FormatInt(in.OldEnd, 10) +
		"&disabled=" + boolFlag(in.Disabled) + "&justplay=" + boolFlag(in.JustPlay) +
		"&afterevent=" + strconv.Itoa(in.AfterEvent) + "&repeated=" + strconv.Itoa(in.Repeated)
	if dir := timerDir(in.Dir); dir != "" {
		path += "&dirname=" + url.QueryEscape(dir)
	}
	if tags := cleanText(in.Tags, 200); tags != "" {
		path += "&tags=" + url.QueryEscape(tags)
	}
	web := newDeviceWeb(profile, 15*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, body, err := web.press(ctx, path)
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type recordNowRequest struct {
	Infinite bool `json:"infinite"`
}

func (s *Server) deviceRecordNow(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var in recordNowRequest
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	path := "/api/recordnow"
	if in.Infinite {
		path += "?infinite=1"
	}
	web := newDeviceWeb(profile, 15*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, body, err := web.press(ctx, path)
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type sleepTimerState struct {
	Enabled bool   `json:"enabled"`
	Minutes int64  `json:"minutes"`
	Action  string `json:"action"`
}

func parseSleepTimer(body []byte) (sleepTimerState, bool) {
	var raw struct {
		Enabled json.RawMessage `json:"enabled"`
		Minutes json.RawMessage `json:"minutes"`
		Action  string          `json:"action"`
	}
	if json.Unmarshal(body, &raw) != nil || raw.Enabled == nil {
		return sleepTimerState{}, false
	}
	state := sleepTimerState{Enabled: rawBool(raw.Enabled), Minutes: rawNum(raw.Minutes), Action: "standby"}
	if strings.EqualFold(strings.TrimSpace(raw.Action), "shutdown") {
		state.Action = "shutdown"
	}
	if state.Minutes < 0 || state.Minutes > 999 {
		state.Minutes = 0
	}
	return state, true
}

func (s *Server) deviceSleepTimerGet(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 10*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, "/api/sleeptimer?cmd=get", 64<<10)
	if writeWebStatus(w, web, status, err) {
		return
	}
	state, ok := parseSleepTimer(body)
	if !ok {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "This receiver does not offer a sleep timer.", "code": "web_error"})
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (s *Server) deviceSleepTimerSet(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var in sleepTimerState
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	path := "/api/sleeptimer?cmd=set&enabled=False"
	if in.Enabled {
		if in.Minutes < 1 || in.Minutes > 999 {
			writeError(w, http.StatusBadRequest, "choose between 1 and 999 minutes")
			return
		}
		action := "standby"
		if in.Action == "shutdown" {
			action = "shutdown"
		} else if in.Action != "" && in.Action != "standby" {
			writeError(w, http.StatusBadRequest, "the action must be standby or shutdown")
			return
		}
		path = "/api/sleeptimer?cmd=set&enabled=True&time=" + strconv.FormatInt(in.Minutes, 10) + "&action=" + action
	}
	web := newDeviceWeb(profile, 10*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, body, err := web.press(ctx, path)
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	if state, ok := parseSleepTimer(body); ok {
		writeJSON(w, http.StatusOK, state)
		return
	}
	writeJSON(w, http.StatusOK, in)
}

func (s *Server) deviceEPGSimilar(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	id, _ := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if !channelRefOK(ref) || id <= 0 {
		writeError(w, http.StatusBadRequest, "not a programme")
		return
	}
	web := newDeviceWeb(profile, 20*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, "/api/epgsimilar?sRef="+url.QueryEscape(ref)+"&eventid="+strconv.FormatInt(id, 10), maxGuideBytes)
	if writeWebStatus(w, web, status, err) {
		return
	}
	var raw struct {
		Events []rawGuideEvent `json:"events"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return similar programmes.", "code": "web_error"})
		return
	}
	events, now := guideEvents(raw.Events, maxSearchEvents, "")
	if now == 0 {
		now = time.Now().Unix()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "events": events})
}

func (s *Server) deviceMovieLocations(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 10*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, "/api/getlocations", 256<<10)
	if writeWebStatus(w, web, status, err) {
		return
	}
	var raw struct {
		Locations []string `json:"locations"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return its recording folders.", "code": "web_error"})
		return
	}
	out := make([]string, 0, len(raw.Locations))
	for _, dir := range raw.Locations {
		if dir = strings.TrimSpace(dir); moviePathOK(dir) && len(out) < 100 {
			out = append(out, dir)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"locations": out})
}

func movieRefOK(ref string) bool {
	parts := strings.SplitN(ref, ":", 11)
	return serviceRefPattern.MatchString(ref) && len(parts) == 11 && moviePathOK(parts[10])
}

type movieRenameRequest struct {
	Ref  string `json:"ref"`
	Name string `json:"name"`
}

func (s *Server) deviceMovieRename(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var in movieRenameRequest
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ref, name := strings.TrimSpace(in.Ref), cleanText(in.Name, 200)
	if !movieRefOK(ref) {
		writeError(w, http.StatusBadRequest, "not a recording")
		return
	}
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, `/\`) {
		writeError(w, http.StatusBadRequest, "the new name must not be empty or contain a slash")
		return
	}
	web := newDeviceWeb(profile, 20*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, body, err := web.press(ctx, "/api/movierename?sRef="+url.QueryEscape(ref)+"&newname="+url.QueryEscape(name))
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type movieMoveRequest struct {
	Ref string `json:"ref"`
	Dir string `json:"dir"`
}

func (s *Server) deviceMovieMove(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var in movieMoveRequest
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ref, dir := strings.TrimSpace(in.Ref), strings.TrimSpace(in.Dir)
	if !movieRefOK(ref) {
		writeError(w, http.StatusBadRequest, "not a recording")
		return
	}
	if !moviePathOK(dir) {
		writeError(w, http.StatusBadRequest, "not a folder on the receiver")
		return
	}
	web := newDeviceWeb(profile, 30*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	status, body, err := web.press(ctx, "/api/moviemove?sRef="+url.QueryEscape(ref)+"&dirname="+url.QueryEscape(dir))
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
