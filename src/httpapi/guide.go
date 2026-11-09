package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/config"
)

const (
	maxGuideBytes   = 12 << 20
	maxGuideEvents  = 400
	maxSearchEvents = 200
	maxTimerSpan    = 48 * 3600
	maxMovieEntries = 2000
)

func rawNum(m json.RawMessage) int64 {
	text := strings.Trim(string(m), `" `)
	if n, err := strconv.ParseInt(text, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(text, 64); err == nil {
		return int64(f)
	}
	return 0
}

func cleanText(text string, limit int) string {
	var b strings.Builder
	count := 0
	for _, r := range strings.TrimSpace(text) {
		if r == '\n' || r == '\t' {
			r = ' '
		}
		if r < ' ' || r == 0x7f {
			continue
		}
		if count++; count > limit {
			break
		}
		b.WriteRune(r)
	}
	return b.String()
}

func moviePathOK(path string) bool {
	if path == "" || len(path) > 900 || !strings.HasPrefix(path, "/") || strings.ContainsAny(path, "\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(path, "/") {
		if part == ".." {
			return false
		}
	}
	return true
}

type guideEvent struct {
	ID       int64  `json:"id"`
	Ref      string `json:"ref"`
	Channel  string `json:"channel"`
	Title    string `json:"title"`
	Short    string `json:"short,omitempty"`
	Long     string `json:"long,omitempty"`
	Begin    int64  `json:"begin"`
	Duration int64  `json:"duration"`
}

type rawGuideEvent struct {
	ID       json.RawMessage `json:"id"`
	Ref      string          `json:"sref"`
	Channel  string          `json:"sname"`
	Title    string          `json:"title"`
	Short    string          `json:"shortdesc"`
	Long     string          `json:"longdesc"`
	Begin    json.RawMessage `json:"begin_timestamp"`
	Duration json.RawMessage `json:"duration_sec"`
	Now      json.RawMessage `json:"now_timestamp"`
}

func guideEvents(raw []rawGuideEvent, limit int, fallbackRef string) ([]guideEvent, int64) {
	out := make([]guideEvent, 0, len(raw))
	var now int64
	for _, e := range raw {
		if n := rawNum(e.Now); n > now {
			now = n
		}
		title := cleanText(e.Title, 300)
		begin, duration := rawNum(e.Begin), rawNum(e.Duration)
		if title == "" || title == "N/A" || begin <= 0 || duration <= 0 {
			continue
		}
		ref := e.Ref
		if ref == "" {
			ref = fallbackRef
		}
		if !serviceRefPattern.MatchString(ref) {
			continue
		}
		out = append(out, guideEvent{
			ID: rawNum(e.ID), Ref: ref, Channel: cleanText(e.Channel, 120), Title: title,
			Short: cleanText(e.Short, 600), Long: cleanText(e.Long, 4000), Begin: begin, Duration: duration,
		})
		if len(out) >= limit {
			break
		}
	}
	return out, now
}

func (s *Server) deviceEPG(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	ref := strings.TrimSpace(r.URL.Query().Get("ref"))
	if !serviceRefPattern.MatchString(ref) || serviceFlags(ref)&64 != 0 {
		writeError(w, http.StatusBadRequest, "not a channel reference")
		return
	}
	web := newDeviceWeb(profile, 20*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, "/api/epgservice?sRef="+url.QueryEscape(ref), maxGuideBytes)
	if writeWebStatus(w, web, status, err) {
		return
	}
	var raw struct {
		Events []rawGuideEvent `json:"events"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return the programme guide.", "code": "web_error"})
		return
	}
	events, now := guideEvents(raw.Events, maxGuideEvents, ref)
	if now == 0 {
		now = time.Now().Unix()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "events": events})
}

func (s *Server) deviceEPGSearch(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	query := cleanText(r.URL.Query().Get("q"), 100)
	if len([]rune(query)) < 2 {
		writeError(w, http.StatusBadRequest, "type at least two letters to search")
		return
	}
	web := newDeviceWeb(profile, 25*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, "/api/epgsearch?search="+url.QueryEscape(query), maxGuideBytes)
	if writeWebStatus(w, web, status, err) {
		return
	}
	var raw struct {
		Events []rawGuideEvent `json:"events"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return search results.", "code": "web_error"})
		return
	}
	events, now := guideEvents(raw.Events, maxSearchEvents, "")
	if now == 0 {
		now = time.Now().Unix()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "events": events})
}

type timerEntry struct {
	Ref         string `json:"ref"`
	Channel     string `json:"channel"`
	Title       string `json:"title"`
	Description string `json:"description,omitempty"`
	Begin       int64  `json:"begin"`
	End         int64  `json:"end"`
	EIT         int64  `json:"eit,omitempty"`
	Disabled    bool   `json:"disabled,omitempty"`
	JustPlay    bool   `json:"justplay,omitempty"`
	State       int64  `json:"state"`
	Repeated    int64  `json:"repeated,omitempty"`
	AfterEvent  int64  `json:"afterevent"`
	File        string `json:"file,omitempty"`
}

func (s *Server) deviceTimers(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 15*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, "/api/timerlist", maxGuideBytes)
	if writeWebStatus(w, web, status, err) {
		return
	}
	var raw struct {
		Timers []struct {
			Ref         string          `json:"serviceref"`
			Channel     string          `json:"servicename"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Begin       json.RawMessage `json:"begin"`
			End         json.RawMessage `json:"end"`
			EIT         json.RawMessage `json:"eit"`
			Disabled    json.RawMessage `json:"disabled"`
			JustPlay    json.RawMessage `json:"justplay"`
			State       json.RawMessage `json:"state"`
			Repeated    json.RawMessage `json:"repeated"`
			AfterEvent  json.RawMessage `json:"afterevent"`
			File        string          `json:"filename"`
		} `json:"timers"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return its timers.", "code": "web_error"})
		return
	}
	out := make([]timerEntry, 0, len(raw.Timers))
	for _, t := range raw.Timers {
		begin, end := rawNum(t.Begin), rawNum(t.End)
		if !serviceRefPattern.MatchString(t.Ref) || begin <= 0 || end <= begin {
			continue
		}
		out = append(out, timerEntry{
			Ref: t.Ref, Channel: cleanText(t.Channel, 120), Title: cleanText(t.Name, 300), Description: cleanText(t.Description, 600),
			Begin: begin, End: end, EIT: rawNum(t.EIT), Disabled: rawNum(t.Disabled) == 1, JustPlay: rawNum(t.JustPlay) == 1,
			State: rawNum(t.State), Repeated: rawNum(t.Repeated), AfterEvent: rawNum(t.AfterEvent), File: cleanText(t.File, 400),
		})
		if len(out) >= 1000 {
			break
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"now": time.Now().Unix(), "timers": out})
}

type timerAddRequest struct {
	Ref         string `json:"ref"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Begin       int64  `json:"begin"`
	End         int64  `json:"end"`
	EventID     int64  `json:"eventid"`
	JustPlay    bool   `json:"justplay"`
	AfterEvent  int    `json:"afterevent"`
}

func (s *Server) deviceTimerAdd(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var input timerAddRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ref := strings.TrimSpace(input.Ref)
	if !serviceRefPattern.MatchString(ref) || serviceFlags(ref)&64 != 0 || strings.ContainsAny(ref, " \"'<>\\") {
		writeError(w, http.StatusBadRequest, "not a channel reference")
		return
	}
	if input.AfterEvent < 0 || input.AfterEvent > 3 {
		input.AfterEvent = 3
	}
	justplay := "0"
	if input.JustPlay {
		justplay = "1"
	}
	var path string
	if input.EventID > 0 {
		path = "/api/timeraddbyeventid?sRef=" + url.QueryEscape(ref) + "&eventid=" + strconv.FormatInt(input.EventID, 10) +
			"&justplay=" + justplay + "&afterevent=" + strconv.Itoa(input.AfterEvent)
	} else {
		name := cleanText(input.Name, 200)
		if name == "" {
			writeError(w, http.StatusBadRequest, "the timer needs a name")
			return
		}
		if input.Begin <= 0 || input.End <= input.Begin || input.End-input.Begin > maxTimerSpan {
			writeError(w, http.StatusBadRequest, "the timer needs a start before its end, at most 48 hours long")
			return
		}
		path = "/api/timeradd?sRef=" + url.QueryEscape(ref) + "&begin=" + strconv.FormatInt(input.Begin, 10) + "&end=" + strconv.FormatInt(input.End, 10) +
			"&name=" + url.QueryEscape(name) + "&description=" + url.QueryEscape(cleanText(input.Description, 300)) +
			"&disabled=0&justplay=" + justplay + "&afterevent=" + strconv.Itoa(input.AfterEvent)
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

type timerRefRequest struct {
	Ref   string `json:"ref"`
	Begin int64  `json:"begin"`
	End   int64  `json:"end"`
	EIT   int64  `json:"eit"`
}

func (s *Server) timerKeyAction(w http.ResponseWriter, r *http.Request, endpoint string) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var input timerRefRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ref := strings.TrimSpace(input.Ref)
	if !serviceRefPattern.MatchString(ref) || input.Begin <= 0 || input.End <= input.Begin {
		writeError(w, http.StatusBadRequest, "not a timer")
		return
	}
	path := endpoint + "?sRef=" + url.QueryEscape(ref) + "&begin=" + strconv.FormatInt(input.Begin, 10) + "&end=" + strconv.FormatInt(input.End, 10)
	if endpoint == "/api/timerdelete" && input.EIT > 0 {
		path += "&eit=" + strconv.FormatInt(input.EIT, 10)
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

func (s *Server) deviceTimerToggle(w http.ResponseWriter, r *http.Request) {
	s.timerKeyAction(w, r, "/api/timertogglestatus")
}

func (s *Server) deviceTimerDelete(w http.ResponseWriter, r *http.Request) {
	s.timerKeyAction(w, r, "/api/timerdelete")
}

func (s *Server) deviceTimerCleanup(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 15*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	status, body, err := web.press(ctx, "/api/timercleanup")
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type movieEntry struct {
	Ref     string `json:"ref"`
	File    string `json:"file"`
	Title   string `json:"title"`
	Channel string `json:"channel,omitempty"`
	Info    string `json:"info,omitempty"`
	Time    int64  `json:"time,omitempty"`
	Length  string `json:"length,omitempty"`
	Seen    int64  `json:"seen,omitempty"`
	Size    int64  `json:"size,omitempty"`
}

func (s *Server) deviceMovies(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	path := "/api/movielist"
	if dir := strings.TrimSpace(r.URL.Query().Get("dir")); dir != "" {
		if !moviePathOK(dir) {
			writeError(w, http.StatusBadRequest, "not a folder on the receiver")
			return
		}
		path += "?dirname=" + url.QueryEscape(dir)
	}
	web := newDeviceWeb(profile, 30*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, path, maxChannelListBytes)
	if writeWebStatus(w, web, status, err) {
		return
	}
	var raw struct {
		Directory string   `json:"directory"`
		Folders   []string `json:"bookmarks"`
		Movies    []struct {
			Ref         string          `json:"serviceref"`
			File        string          `json:"filename"`
			Event       string          `json:"eventname"`
			Channel     string          `json:"servicename"`
			Description string          `json:"description"`
			Time        json.RawMessage `json:"recordingtime"`
			Length      string          `json:"length"`
			Seen        json.RawMessage `json:"lastseen"`
			Size        json.RawMessage `json:"filesize"`
		} `json:"movies"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return its recordings.", "code": "web_error"})
		return
	}
	movies := make([]movieEntry, 0, len(raw.Movies))
	for _, m := range raw.Movies {
		if !moviePathOK(m.File) || !serviceRefPattern.MatchString(m.Ref) {
			continue
		}
		title := cleanText(m.Event, 300)
		if title == "" {
			title = m.File[strings.LastIndex(m.File, "/")+1:]
		}
		length := cleanText(m.Length, 12)
		if length == "?:??" {
			length = ""
		}
		movies = append(movies, movieEntry{
			Ref: m.Ref, File: m.File, Title: title, Channel: cleanText(m.Channel, 120), Info: cleanText(m.Description, 400),
			Time: rawNum(m.Time), Length: length, Seen: rawNum(m.Seen), Size: rawNum(m.Size),
		})
		if len(movies) >= maxMovieEntries {
			break
		}
	}
	folders := make([]string, 0, len(raw.Folders))
	for _, name := range raw.Folders {
		if name = cleanText(name, 200); name != "" && !strings.Contains(name, "/") && name != "." && name != ".." {
			folders = append(folders, name)
		}
	}
	directory := raw.Directory
	if !moviePathOK(directory) {
		directory = ""
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"directory": directory, "folders": folders, "movies": movies})
}

type movieDeleteRequest struct {
	Ref string `json:"ref"`
}

func (s *Server) deviceMovieDelete(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var input movieDeleteRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ref := strings.TrimSpace(input.Ref)
	parts := strings.SplitN(ref, ":", 11)
	if !serviceRefPattern.MatchString(ref) || len(parts) < 11 || !moviePathOK(parts[10]) {
		writeError(w, http.StatusBadRequest, "not a recording")
		return
	}
	web := newDeviceWeb(profile, 20*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, body, err := web.press(ctx, "/api/moviedelete?sRef="+url.QueryEscape(ref))
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func movieM3U(profile config.Profile, file, name string) string {
	web := newDeviceWeb(profile, time.Second)
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	if name != "" {
		b.WriteString("#EXTINF:-1," + name + "\n")
	}
	b.WriteString(web.base + "/file?file=" + url.QueryEscape(file) + "\n")
	return b.String()
}

func (s *Server) deviceMovieStream(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	file := strings.TrimSpace(r.URL.Query().Get("file"))
	if !moviePathOK(file) {
		writeError(w, http.StatusBadRequest, "not a recording")
		return
	}
	name := cleanChannelName(r.URL.Query().Get("name"))
	playlist := ""
	web := newDeviceWeb(profile, 5*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	if status, body, err := web.getBounded(ctx, "/web/ts.m3u?file="+url.QueryEscape(file), 8192); err == nil && status == http.StatusOK {
		if text := strings.TrimSpace(string(body)); strings.HasPrefix(text, "#EXTM3U") && strings.Contains(text, "http") {
			playlist = text + "\n"
		}
	}
	if playlist == "" {
		playlist = movieM3U(profile, file, name)
	}
	base := name
	if base == "" {
		base = file[strings.LastIndex(file, "/")+1:]
	}
	base = strings.Trim(m3uFileName.ReplaceAllString(base, "_"), "._-")
	if base == "" {
		base = "recording"
	}
	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Header().Set("Content-Disposition", `attachment; filename="`+base+`.m3u"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(playlist))
}
