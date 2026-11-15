package httpapi

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"github.com/didou07/dcc-lite/src/config"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// EPGRefresh keeps the receiver's guide fresh by visiting channels (OpenWebif /epgrefresh).
type erSettingsXML struct {
	Settings []struct {
		Name  string `xml:"e2settingname"`
		Value string `xml:"e2settingvalue"`
	} `xml:"e2setting"`
}

type erServicesXML struct {
	Services []struct {
		Ref  string `xml:"e2servicereference"`
		Name string `xml:"e2servicename"`
	} `xml:"e2service"`
}

type erService struct {
	Ref     string `json:"ref"`
	Name    string `json:"name"`
	Bouquet bool   `json:"bouquet,omitempty"`
}

type erSettings struct {
	Enabled        bool        `json:"enabled"`
	ShowMessage    bool        `json:"show_message"`
	Begin          string      `json:"begin"`
	End            string      `json:"end"`
	SecondsPerChan int         `json:"seconds_per_channel"`
	DelayStandby   int         `json:"delay_standby"`
	InheritAT      bool        `json:"inherit_autotimer"`
	ShutdownAfter  bool        `json:"shutdown_after"`
	Force          bool        `json:"force"`
	Wakeup         bool        `json:"wakeup"`
	RunAutoTimer   string      `json:"run_autotimer"`
	Adapter        string      `json:"adapter"`
	SkipProtected  string      `json:"skip_protected"`
	LastScan       int64       `json:"last_scan,omitempty"`
	CanBackground  bool        `json:"can_background"`
	HasAutoTimer   bool        `json:"has_autotimer"`
	Services       []erService `json:"services"`
}

func erBool(v string) bool { return strings.EqualFold(strings.TrimSpace(v), "true") }

// receiverOffset derives the receiver's UTC offset from its clock, rounded to 15 minutes.
func receiverOffset(local string, now time.Time) (time.Duration, bool) {
	parts := strings.Split(strings.TrimSpace(local), ":")
	if len(parts) < 2 {
		return 0, false
	}
	h, errH := strconv.Atoi(strings.TrimSpace(parts[0]))
	m, errM := strconv.Atoi(strings.TrimSpace(parts[1]))
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	utc := now.UTC()
	diff := (h*3600 + m*60) - (utc.Hour()*3600 + utc.Minute()*60 + utc.Second())
	diff = ((diff % 86400) + 86400) % 86400
	if diff > 14*3600 {
		diff -= 86400
	}
	diff = int(float64(diff)/900.0+0.5*sign(diff)) * 900
	return time.Duration(diff) * time.Second, true
}

func sign(n int) float64 {
	if n < 0 {
		return -1
	}
	return 1
}

// clockText turns the epoch the plugin reports (today at HH:MM, receiver time) into "HH:MM".
func clockText(epoch int64, offset time.Duration) string {
	secs := ((epoch+int64(offset/time.Second))%86400 + 86400) % 86400
	return fmt.Sprintf("%02d:%02d", secs/3600, (secs%3600)/60)
}

// clockEpoch maps any day's HH:MM in receiver time to an epoch (all the plugin reads).
func clockEpoch(text string, offset time.Duration, now time.Time) (int64, bool) {
	if !hhmmPattern.MatchString(text) {
		return 0, false
	}
	parts := strings.SplitN(text, ":", 2)
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	day := now.UTC().Unix() / 86400 * 86400
	return day + int64(h*3600+m*60) - int64(offset/time.Second), true
}

func parseERSettings(settings, services []byte, offset time.Duration) (erSettings, error) {
	var sx erSettingsXML
	dec := xml.NewDecoder(bytes.NewReader(settings))
	dec.Strict = false
	if err := dec.Decode(&sx); err != nil {
		return erSettings{}, err
	}
	v := map[string]string{}
	for _, s := range sx.Settings {
		v[strings.TrimPrefix(strings.TrimSpace(s.Name), "config.plugins.epgrefresh.")] = strings.TrimSpace(s.Value)
	}
	num := func(key string) int { n, _ := strconv.Atoi(v[key]); return n }
	epoch := func(key string) int64 { n, _ := strconv.ParseInt(v[key], 10, 64); return n }
	out := erSettings{
		Enabled: erBool(v["enabled"]), ShowMessage: erBool(v["enablemessage"]),
		Begin: clockText(epoch("begin"), offset), End: clockText(epoch("end"), offset),
		SecondsPerChan: num("interval_seconds"), DelayStandby: num("delay_standby"),
		InheritAT: erBool(v["inherit_autotimer"]), ShutdownAfter: erBool(v["afterevent"]), Force: erBool(v["force"]), Wakeup: erBool(v["wakeup"]),
		RunAutoTimer: v["parse_autotimer"], Adapter: v["adapter"], SkipProtected: v["skipProtectedServices"], LastScan: epoch("lastscan"),
		CanBackground: erBool(v["canDoBackgroundRefresh"]), HasAutoTimer: erBool(v["hasAutoTimer"]), Services: []erService{},
	}
	var lx erServicesXML
	ldec := xml.NewDecoder(bytes.NewReader(services))
	ldec.Strict = false
	if err := ldec.Decode(&lx); err == nil {
		for _, s := range lx.Services {
			ref := strings.TrimSpace(s.Ref)
			if ref == "" || len(out.Services) >= 1000 {
				continue
			}
			out.Services = append(out.Services, erService{Ref: ref, Name: cleanText(s.Name, 120), Bouquet: strings.Contains(ref, "FROM BOUQUET")})
		}
	}
	return out, nil
}

func (s *Server) erFetch(w http.ResponseWriter, r *http.Request, profile config.Profile) (erSettings, bool) {
	web := newDeviceWeb(profile, 20*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, "/epgrefresh/get", 1<<20)
	if err == nil && status == http.StatusNotFound {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "The EPGRefresh plugin is not installed or not active on this receiver.", "code": "no_epgrefresh"})
		return erSettings{}, false
	}
	if writeWebStatus(w, web, status, err) {
		return erSettings{}, false
	}
	status, list, err := web.getBounded(ctx, "/epgrefresh", 4<<20)
	if writeWebStatus(w, web, status, err) {
		return erSettings{}, false
	}
	offset := time.Duration(0)
	if st, m, err := getJSONMap(ctx, web, "/api/currenttime"); err == nil && st == http.StatusOK {
		if o, ok := receiverOffset(jsonText(m["time"]), time.Now()); ok {
			offset = o
		}
	}
	settings, err := parseERSettings(body, list, offset)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The EPGRefresh plugin did not return its settings.", "code": "no_epgrefresh"})
		return erSettings{}, false
	}
	return settings, true
}

func (s *Server) deviceEPGRefresh(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	settings, ok := s.erFetch(w, r, profile)
	if !ok {
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, settings)
}

type erSaveRequest struct {
	Enabled        bool     `json:"enabled"`
	ShowMessage    bool     `json:"show_message"`
	Begin          string   `json:"begin"`
	End            string   `json:"end"`
	SecondsPerChan int      `json:"seconds_per_channel"`
	DelayStandby   int      `json:"delay_standby"`
	InheritAT      bool     `json:"inherit_autotimer"`
	ShutdownAfter  bool     `json:"shutdown_after"`
	Force          bool     `json:"force"`
	Wakeup         bool     `json:"wakeup"`
	RunAutoTimer   string   `json:"run_autotimer"`
	Adapter        string   `json:"adapter"`
	SkipProtected  string   `json:"skip_protected"`
	Services       []string `json:"services"`
}

func erFlag(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func buildERQuery(in erSaveRequest, offset time.Duration, now time.Time) (string, error) {
	begin, ok1 := clockEpoch(in.Begin, offset, now)
	end, ok2 := clockEpoch(in.End, offset, now)
	if !ok1 || !ok2 {
		return "", fmt.Errorf("the start and end times must look like 02:30")
	}
	if in.SecondsPerChan < 5 || in.SecondsPerChan > 3600 {
		return "", fmt.Errorf("the time on each channel must be between 5 and 3600 seconds")
	}
	if in.DelayStandby < 1 || in.DelayStandby > 240 {
		return "", fmt.Errorf("the wait after standby must be between 1 and 240 minutes")
	}
	switch in.RunAutoTimer {
	case "always", "never", "bg_only", "ask_yes", "ask_no":
	default:
		return "", fmt.Errorf("unknown 'run AutoTimer afterwards' choice")
	}
	switch in.Adapter {
	case "main", "pip", "pip_hidden", "record":
	default:
		return "", fmt.Errorf("unknown refresh method")
	}
	switch in.SkipProtected {
	case "bg_only", "always":
	default:
		return "", fmt.Errorf("unknown choice for protected channels")
	}
	var q strings.Builder
	q.WriteString("enabled=" + erFlag(in.Enabled))
	q.WriteString("&enablemessage=" + erFlag(in.ShowMessage))
	q.WriteString("&begin=" + strconv.FormatInt(begin, 10) + "&end=" + strconv.FormatInt(end, 10))
	q.WriteString("&interval_seconds=" + strconv.Itoa(in.SecondsPerChan))
	q.WriteString("&delay_standby=" + strconv.Itoa(in.DelayStandby))
	q.WriteString("&inherit_autotimer=" + erFlag(in.InheritAT))
	q.WriteString("&afterevent=" + erFlag(in.ShutdownAfter))
	q.WriteString("&force=" + erFlag(in.Force))
	q.WriteString("&wakeup=" + erFlag(in.Wakeup))
	q.WriteString("&parse_autotimer=" + in.RunAutoTimer)
	q.WriteString("&adapter=" + in.Adapter)
	q.WriteString("&skipProtectedServices=" + in.SkipProtected)
	return q.String(), nil
}

func erResult(body []byte) (bool, string) {
	res, err := parseATResult(body)
	if err != nil {
		return false, "The EPGRefresh plugin gave an answer that could not be read."
	}
	return atBool(res.State), cleanText(strings.ReplaceAll(res.Text, "\n", " "), 300)
}

func (s *Server) deviceEPGRefreshSave(w http.ResponseWriter, r *http.Request) {
	var in erSaveRequest
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if len(in.Services) == 0 && in.Enabled {
		writeError(w, http.StatusBadRequest, "choose at least one channel or bouquet")
		return
	}
	if len(in.Services) > 200 {
		writeError(w, http.StatusBadRequest, "too many channels or bouquets selected")
		return
	}
	for _, ref := range in.Services {
		if !(serviceRefPattern.MatchString(ref) || bouquetRefPattern.MatchString(ref)) || strings.ContainsAny(ref, "&#%") {
			writeError(w, http.StatusBadRequest, "a selected channel or bouquet is not valid")
			return
		}
	}
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 30*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	offset := time.Duration(0)
	if st, m, err := getJSONMap(ctx, web, "/api/currenttime"); err == nil && st == http.StatusOK {
		if o, ok := receiverOffset(jsonText(m["time"]), time.Now()); ok {
			offset = o
		}
	}
	query, err := buildERQuery(in, offset, time.Now())
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	// Channels first: "multi" makes the plugin forget the old list before it stores the new one.
	if len(in.Services) > 0 {
		var sq strings.Builder
		sq.WriteString("multi=1")
		for _, ref := range in.Services {
			sq.WriteString("&sref=" + url.QueryEscape(ref))
		}
		status, body, err := web.getBounded(ctx, "/epgrefresh/add?"+sq.String(), 1<<20)
		if err == nil && status == http.StatusNotFound {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "The EPGRefresh plugin is not installed or not active on this receiver.", "code": "no_epgrefresh"})
			return
		}
		if writeWebStatus(w, web, status, err) {
			return
		}
		_ = body
	}
	status, body, err := web.getBounded(ctx, "/epgrefresh/set?"+query, 1<<20)
	if writeWebStatus(w, web, status, err) {
		return
	}
	if ok, text := erResult(body); !ok {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": text, "code": "epgrefresh_refused"})
		return
	}
	// Read the settings back: the plugin silently ignores a choice the receiver does not support.
	saved, ok := s.erFetch(w, r, profile)
	if !ok {
		return
	}
	notes := []string{}
	if saved.Adapter != in.Adapter {
		notes = append(notes, "This receiver does not support that refresh method, so it kept “"+saved.Adapter+"”.")
	}
	if saved.Enabled != in.Enabled {
		notes = append(notes, "The receiver did not apply the on/off choice.")
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "settings": saved, "notes": notes})
}

func (s *Server) deviceEPGRefreshNow(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 20*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	status, body, err := web.getBounded(ctx, "/epgrefresh/refresh", 1<<20)
	if err == nil && status == http.StatusNotFound {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "The EPGRefresh plugin is not installed or not active on this receiver.", "code": "no_epgrefresh"})
		return
	}
	if writeWebStatus(w, web, status, err) {
		return
	}
	ok, text := erResult(body)
	if !ok {
		if text == "" {
			text = "The receiver could not start the refresh."
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": text, "code": "epgrefresh_refused"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": text})
}
