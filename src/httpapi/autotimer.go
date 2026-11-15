package httpapi

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// The AutoTimer plugin publishes its own web API under /autotimer on the receiver's web interface.
// Rules are listed as XML and changed with plain query parameters.
const (
	maxAutoTimerBytes = 4 << 20
	maxAutoRules      = 500
	maxAutoPreview    = 500
	maxAutoList       = 30
)

var (
	hhmmPattern  = regexp.MustCompile(`^([01]?[0-9]|2[0-3]):[0-5][0-9]$`)
	atDayPattern = regexp.MustCompile(`^([0-6]|weekend|weekday)$`)
)

type atXMLTimer struct {
	Name       string `xml:"name,attr"`
	Match      string `xml:"match,attr"`
	Enabled    string `xml:"enabled,attr"`
	ID         int    `xml:"id,attr"`
	From       string `xml:"from,attr"`
	To         string `xml:"to,attr"`
	After      string `xml:"after,attr"`
	Before     string `xml:"before,attr"`
	MaxDur     string `xml:"maxduration,attr"`
	Location   string `xml:"location,attr"`
	Offset     string `xml:"offset,attr"`
	Counter    string `xml:"counter,attr"`
	Left       string `xml:"left,attr"`
	AvoidDup   string `xml:"avoidDuplicateDescription,attr"`
	JustPlay   string `xml:"justplay,attr"`
	SearchType string `xml:"searchType,attr"`
	SearchCase string `xml:"searchCase,attr"`
	AlwaysZap  string `xml:"always_zap,attr"`
	Services   []struct {
		Ref  string `xml:"e2servicereference"`
		Name string `xml:"e2servicename"`
	} `xml:"e2service"`
	AfterEvent []struct {
		From string `xml:"from,attr"`
		To   string `xml:"to,attr"`
		Text string `xml:",chardata"`
	} `xml:"afterevent"`
	Include []atXMLFilter `xml:"include"`
	Exclude []atXMLFilter `xml:"exclude"`
	Tags    []string      `xml:"e2tag"`
}

type atXMLFilter struct {
	Where string `xml:"where,attr"`
	Text  string `xml:",chardata"`
}

type atXMLList struct {
	Timers []atXMLTimer `xml:"timer"`
}

type atService struct {
	Ref     string `json:"ref"`
	Name    string `json:"name"`
	Bouquet bool   `json:"bouquet,omitempty"`
}

type atFilters struct {
	Title []string `json:"title"`
	Short []string `json:"short"`
	Desc  []string `json:"desc"`
	Days  []string `json:"days"`
}

type atRule struct {
	ID          int         `json:"id"`
	Name        string      `json:"name"`
	Match       string      `json:"match"`
	Enabled     bool        `json:"enabled"`
	SearchType  string      `json:"search_type"`
	CaseSens    bool        `json:"case_sensitive"`
	Services    []atService `json:"services"`
	JustPlay    bool        `json:"justplay"`
	AlwaysZap   bool        `json:"always_zap"`
	Location    string      `json:"location,omitempty"`
	OffsetBegin *int        `json:"offset_before,omitempty"`
	OffsetEnd   *int        `json:"offset_after,omitempty"`
	MaxDuration int         `json:"max_duration,omitempty"`
	SpanFrom    string      `json:"span_from,omitempty"`
	SpanTo      string      `json:"span_to,omitempty"`
	Counter     int         `json:"counter,omitempty"`
	Left        int         `json:"left,omitempty"`
	AvoidDup    int         `json:"avoid_duplicates,omitempty"`
	AfterEvent  string      `json:"afterevent"`
	Include     atFilters   `json:"include"`
	Exclude     atFilters   `json:"exclude"`
	Tags        []string    `json:"tags"`
}

func atBool(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "yes", "1", "true":
		return true
	}
	return false
}

func atInt(v string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(v))
	return n
}

func atFilterSplit(list []atXMLFilter) atFilters {
	out := atFilters{Title: []string{}, Short: []string{}, Desc: []string{}, Days: []string{}}
	for _, f := range list {
		text := cleanText(f.Text, 300)
		if text == "" {
			continue
		}
		switch f.Where {
		case "title":
			out.Title = append(out.Title, text)
		case "shortdescription":
			out.Short = append(out.Short, text)
		case "description":
			out.Desc = append(out.Desc, text)
		case "dayofweek":
			if atDayPattern.MatchString(text) {
				out.Days = append(out.Days, text)
			}
		}
	}
	return out
}

func atAfterEvent(raw []struct {
	From string `xml:"from,attr"`
	To   string `xml:"to,attr"`
	Text string `xml:",chardata"`
}) string {
	if len(raw) == 0 {
		return "default"
	}
	switch strings.TrimSpace(raw[0].Text) {
	case "none":
		return "nothing"
	case "standby":
		return "standby"
	case "shutdown":
		return "deepstandby"
	case "auto":
		return "auto"
	}
	return "default"
}

func parseAutoTimers(body []byte) ([]atRule, error) {
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	var list atXMLList
	if err := dec.Decode(&list); err != nil {
		return nil, err
	}
	rules := make([]atRule, 0, len(list.Timers))
	for _, t := range list.Timers {
		if len(rules) >= maxAutoRules {
			break
		}
		r := atRule{
			ID: t.ID, Name: cleanText(t.Name, 200), Match: cleanText(t.Match, 300), Enabled: atBool(t.Enabled),
			SearchType: t.SearchType, CaseSens: t.SearchCase == "sensitive", JustPlay: atBool(t.JustPlay),
			AlwaysZap: atBool(t.AlwaysZap), Location: cleanText(t.Location, 400), MaxDuration: atInt(t.MaxDur),
			Counter: atInt(t.Counter), Left: atInt(t.Left), AvoidDup: atInt(t.AvoidDup),
			AfterEvent: atAfterEvent(t.AfterEvent), Include: atFilterSplit(t.Include), Exclude: atFilterSplit(t.Exclude),
			Services: []atService{}, Tags: []string{},
		}
		switch r.SearchType {
		case "exact", "start", "description":
		default:
			r.SearchType = "partial"
		}
		if hhmmPattern.MatchString(t.From) && hhmmPattern.MatchString(t.To) {
			r.SpanFrom, r.SpanTo = t.From, t.To
		}
		if t.Offset != "" {
			parts := strings.SplitN(t.Offset, ",", 2)
			b := atInt(parts[0])
			e := b
			if len(parts) == 2 {
				e = atInt(parts[1])
			}
			r.OffsetBegin, r.OffsetEnd = &b, &e
		}
		for _, s := range t.Services {
			ref := strings.TrimSpace(s.Ref)
			if ref == "" {
				continue
			}
			r.Services = append(r.Services, atService{Ref: ref, Name: cleanText(s.Name, 120), Bouquet: strings.Contains(ref, "FROM BOUQUET")})
		}
		for _, tag := range t.Tags {
			if tag = cleanText(tag, 60); tag != "" {
				r.Tags = append(r.Tags, tag)
			}
		}
		rules = append(rules, r)
	}
	return rules, nil
}

type atResult struct {
	XMLName xml.Name `xml:"e2simplexmlresult"`
	State   string   `xml:"e2state"`
	Text    string   `xml:"e2statetext"`
	ID      string   `xml:"e2id"`
}

func parseATResult(body []byte) (atResult, error) {
	var res atResult
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	err := dec.Decode(&res)
	return res, err
}

// atEsc protects a literal '%': the plugin runs unquote() on the values it receives.
func atEsc(s string) string { return strings.ReplaceAll(s, "%", "%25") }

// autoTimerGet fetches one plugin URL; it reports a missing plugin as a clear error.
func (s *Server) autoTimerGet(w http.ResponseWriter, r *http.Request, path string, timeout time.Duration) ([]byte, bool) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return nil, false
	}
	web := newDeviceWeb(profile, timeout)
	ctx, cancel := context.WithTimeout(r.Context(), timeout)
	defer cancel()
	status, body, err := web.getBounded(ctx, path, maxAutoTimerBytes)
	if err == nil && status == http.StatusNotFound {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "The AutoTimer plugin is not installed or not active on this receiver.", "code": "no_autotimer"})
		return nil, false
	}
	if writeWebStatus(w, web, status, err) {
		return nil, false
	}
	return body, true
}

func (s *Server) deviceAutoTimers(w http.ResponseWriter, r *http.Request) {
	body, ok := s.autoTimerGet(w, r, "/autotimer?webif=true", 20*time.Second)
	if !ok {
		return
	}
	rules, err := parseAutoTimers(body)
	if err != nil {
		if res, rerr := parseATResult(body); rerr == nil && res.Text != "" {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": cleanText(res.Text, 300), "code": "web_error"})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The AutoTimer plugin did not return its rules.", "code": "no_autotimer"})
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"rules": rules})
}

type atSaveRequest struct {
	ID          *int      `json:"id"`
	Name        string    `json:"name"`
	Match       string    `json:"match"`
	Enabled     bool      `json:"enabled"`
	SearchType  string    `json:"search_type"`
	CaseSens    bool      `json:"case_sensitive"`
	Services    []string  `json:"services"`
	Bouquets    []string  `json:"bouquets"`
	JustPlay    bool      `json:"justplay"`
	AlwaysZap   bool      `json:"always_zap"`
	Location    string    `json:"location"`
	OffsetBegin *int      `json:"offset_before"`
	OffsetEnd   *int      `json:"offset_after"`
	MaxDuration int       `json:"max_duration"`
	SpanFrom    string    `json:"span_from"`
	SpanTo      string    `json:"span_to"`
	Counter     int       `json:"counter"`
	AvoidDup    int       `json:"avoid_duplicates"`
	AfterEvent  string    `json:"afterevent"`
	Include     atFilters `json:"include"`
	Exclude     atFilters `json:"exclude"`
	Tags        []string  `json:"tags"`
}

func atList(name string, values []string, keep func(string) bool) (string, error) {
	if len(values) > maxAutoList {
		return "", fmt.Errorf("too many entries for %s (at most %d)", name, maxAutoList)
	}
	var q strings.Builder
	any := false
	for _, v := range values {
		v = cleanText(v, 200)
		if v == "" {
			continue
		}
		if keep != nil && !keep(v) {
			return "", fmt.Errorf("%q is not valid for %s", v, name)
		}
		q.WriteString("&" + url.QueryEscape(name) + "=" + url.QueryEscape(atEsc(v)))
		any = true
	}
	if !any {
		// An empty value is how the plugin is told to clear the list.
		q.WriteString("&" + url.QueryEscape(name) + "=")
	}
	return q.String(), nil
}

// buildAutoTimerQuery validates a rule and turns it into the plugin's edit parameters.
func buildAutoTimerQuery(in atSaveRequest) (string, error) {
	match := cleanText(in.Match, 300)
	if match == "" {
		return "", fmt.Errorf("the rule needs something to search for")
	}
	name := cleanText(in.Name, 200)
	if name == "" {
		name = match
	}
	switch in.SearchType {
	case "partial", "exact", "start", "description":
	default:
		return "", fmt.Errorf("unknown search type")
	}
	var q strings.Builder
	if in.ID != nil {
		if *in.ID < 0 {
			return "", fmt.Errorf("not a valid rule number")
		}
		q.WriteString("id=" + strconv.Itoa(*in.ID) + "&")
	}
	q.WriteString("match=" + url.QueryEscape(atEsc(match)) + "&name=" + url.QueryEscape(atEsc(name)))
	q.WriteString("&enabled=" + boolFlag(in.Enabled))
	q.WriteString("&searchType=" + in.SearchType)
	if in.CaseSens {
		q.WriteString("&searchCase=sensitive")
	} else {
		q.WriteString("&searchCase=insensitive")
	}
	if len(in.Services) > 200 || len(in.Bouquets) > 50 {
		return "", fmt.Errorf("too many channels or bouquets selected")
	}
	for _, ref := range in.Services {
		if !serviceRefPattern.MatchString(ref) || strings.Contains(ref, ",") {
			return "", fmt.Errorf("a selected channel is not valid")
		}
	}
	for _, ref := range in.Bouquets {
		if !bouquetRefPattern.MatchString(ref) || strings.Contains(ref, ",") {
			return "", fmt.Errorf("a selected bouquet is not valid")
		}
	}
	q.WriteString("&services=" + url.QueryEscape(strings.Join(in.Services, ",")))
	q.WriteString("&bouquets=" + url.QueryEscape(strings.Join(in.Bouquets, ",")))
	q.WriteString("&justplay=" + boolFlag(in.JustPlay))
	q.WriteString("&always_zap=" + boolFlag(in.AlwaysZap && !in.JustPlay))
	switch in.AfterEvent {
	case "default", "nothing", "standby", "deepstandby", "auto":
		q.WriteString("&afterevent=" + in.AfterEvent)
	default:
		return "", fmt.Errorf("unknown 'when it is over' action")
	}
	if in.Location != "" && !moviePathOK(in.Location) {
		return "", fmt.Errorf("the recording folder must be a full path such as /media/hdd/movie/")
	}
	q.WriteString("&location=" + url.QueryEscape(in.Location))
	if in.OffsetBegin != nil && in.OffsetEnd != nil {
		if *in.OffsetBegin < 0 || *in.OffsetEnd < 0 || *in.OffsetBegin > 600 || *in.OffsetEnd > 600 {
			return "", fmt.Errorf("start and end margins must be between 0 and 600 minutes")
		}
		q.WriteString(fmt.Sprintf("&offset=%d,%d", *in.OffsetBegin, *in.OffsetEnd))
	} else {
		q.WriteString("&offset=")
	}
	if in.MaxDuration < 0 || in.MaxDuration > 24*60 {
		return "", fmt.Errorf("the longest duration must be between 0 and 1440 minutes")
	}
	if in.MaxDuration > 0 {
		q.WriteString("&maxduration=" + strconv.Itoa(in.MaxDuration))
	} else {
		q.WriteString("&maxduration=")
	}
	switch {
	case in.SpanFrom == "" && in.SpanTo == "":
		q.WriteString("&timespanFrom=&timespanTo=")
	case hhmmPattern.MatchString(in.SpanFrom) && hhmmPattern.MatchString(in.SpanTo):
		q.WriteString("&timespanFrom=" + url.QueryEscape(in.SpanFrom) + "&timespanTo=" + url.QueryEscape(in.SpanTo))
	default:
		return "", fmt.Errorf("the time window needs both times as HH:MM")
	}
	if in.Counter < 0 || in.Counter > 999 {
		return "", fmt.Errorf("the recording limit must be between 0 and 999")
	}
	q.WriteString("&counter=" + strconv.Itoa(in.Counter))
	if in.AvoidDup < 0 || in.AvoidDup > 3 {
		return "", fmt.Errorf("unknown duplicate-avoidance level")
	}
	q.WriteString("&avoidDuplicateDescription=" + strconv.Itoa(in.AvoidDup))
	for _, f := range []struct {
		prefix string
		set    atFilters
	}{{"", in.Include}, {"!", in.Exclude}} {
		for _, item := range []struct {
			key    string
			values []string
			keep   func(string) bool
		}{{"title", f.set.Title, nil}, {"shortdescription", f.set.Short, nil}, {"description", f.set.Desc, nil}, {"dayofweek", f.set.Days, atDayPattern.MatchString}} {
			part, err := atList(f.prefix+item.key, item.values, item.keep)
			if err != nil {
				return "", err
			}
			q.WriteString(part)
		}
	}
	tags := make([]string, 0, len(in.Tags))
	for _, t := range in.Tags {
		if t = cleanText(t, 60); t != "" && !strings.ContainsAny(t, " ,") {
			tags = append(tags, t)
		}
	}
	part, err := atList("tag", tags, nil)
	if err != nil {
		return "", err
	}
	q.WriteString(part)
	return q.String(), nil
}

func (s *Server) deviceAutoTimerSave(w http.ResponseWriter, r *http.Request) {
	var in atSaveRequest
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	query, err := buildAutoTimerQuery(in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	s.autoTimerAction(w, r, "/autotimer/edit?"+query, 30*time.Second)
}

type atIDRequest struct {
	ID *int `json:"id"`
}

func (s *Server) deviceAutoTimerDelete(w http.ResponseWriter, r *http.Request) {
	var in atIDRequest
	if err := decodeJSON(w, r, &in); err != nil || in.ID == nil || *in.ID < 0 {
		writeError(w, http.StatusBadRequest, "which rule?")
		return
	}
	s.autoTimerAction(w, r, "/autotimer/remove?id="+strconv.Itoa(*in.ID), 20*time.Second)
}

// deviceAutoTimerRun asks the plugin to search the guide now and create the timers.
func (s *Server) deviceAutoTimerRun(w http.ResponseWriter, r *http.Request) {
	var in atIDRequest
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	path := "/autotimer/parse"
	if in.ID != nil && *in.ID >= 0 {
		path += "?id=" + strconv.Itoa(*in.ID)
	}
	s.autoTimerAction(w, r, path, 120*time.Second)
}

func (s *Server) autoTimerAction(w http.ResponseWriter, r *http.Request, path string, timeout time.Duration) {
	body, ok := s.autoTimerGet(w, r, path, timeout)
	if !ok {
		return
	}
	res, err := parseATResult(body)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The AutoTimer plugin gave an answer that could not be read.", "code": "web_error"})
		return
	}
	text := cleanText(strings.ReplaceAll(res.Text, "\n", " "), 400)
	if !atBool(res.State) {
		if text == "" {
			text = "The AutoTimer plugin refused the request."
		}
		writeJSON(w, http.StatusUnprocessableEntity, map[string]any{"error": text, "code": "autotimer_refused"})
		return
	}
	id, _ := strconv.Atoi(strings.TrimSpace(res.ID))
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "message": text, "id": id})
}

type atSimTimer struct {
	Ref     string `json:"ref"`
	Channel string `json:"channel"`
	Title   string `json:"title"`
	Begin   int64  `json:"begin"`
	End     int64  `json:"end"`
	Rule    string `json:"rule"`
	Skipped bool   `json:"skipped,omitempty"`
	Message string `json:"message,omitempty"`
}

type atSimXML struct {
	Timers []struct {
		Ref     string `xml:"e2servicereference"`
		Channel string `xml:"e2servicename"`
		Name    string `xml:"e2name"`
		Begin   string `xml:"e2timebegin"`
		End     string `xml:"e2timeend"`
		Rule    string `xml:"e2autotimername"`
		State   string `xml:"e2state"`
		Message string `xml:"e2message"`
	} `xml:"e2simulatedtimer"`
}

// deviceAutoTimerPreview lists what the rules would record, without creating anything.
func (s *Server) deviceAutoTimerPreview(w http.ResponseWriter, r *http.Request) {
	path := "/autotimer/simulate"
	if raw := r.URL.Query().Get("id"); raw != "" {
		id, err := strconv.Atoi(raw)
		if err != nil || id < 0 {
			writeError(w, http.StatusBadRequest, "which rule?")
			return
		}
		path += "?id=" + strconv.Itoa(id)
	}
	body, ok := s.autoTimerGet(w, r, path, 120*time.Second)
	if !ok {
		return
	}
	var sim atSimXML
	dec := xml.NewDecoder(bytes.NewReader(body))
	dec.Strict = false
	if err := dec.Decode(&sim); err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The AutoTimer plugin did not return a preview.", "code": "web_error"})
		return
	}
	out := make([]atSimTimer, 0, len(sim.Timers))
	for _, t := range sim.Timers {
		if len(out) >= maxAutoPreview {
			break
		}
		begin, _ := strconv.ParseInt(strings.TrimSpace(t.Begin), 10, 64)
		end, _ := strconv.ParseInt(strings.TrimSpace(t.End), 10, 64)
		out = append(out, atSimTimer{
			Ref: strings.TrimSpace(t.Ref), Channel: cleanText(t.Channel, 120), Title: cleanText(t.Name, 300), Begin: begin, End: end,
			Rule: cleanText(t.Rule, 200), Skipped: strings.EqualFold(strings.TrimSpace(t.State), "skip"), Message: cleanText(t.Message, 300),
		})
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"timers": out})
}
