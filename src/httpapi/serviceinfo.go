package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/config"
)

type infoPair struct {
	K string `json:"k"`
	V string `json:"v"`
}

// ecmLabels lists the ecm.info lines worth showing, in display order.
// Control words (cw0/cw1) are deliberately left out.
var ecmLabels = []struct{ key, label string }{
	{"caid", "CAID"}, {"provid", "Provider ID"}, {"prov", "Provider ID"}, {"pid", "ECM PID"}, {"chid", "CHID"},
	{"reader", "Reader"}, {"from", "From"}, {"source", "Source"}, {"address", "Address"},
	{"protocol", "Protocol"}, {"system", "System"}, {"using", "Using"}, {"hops", "Hops"},
	{"ecm time", "ECM time"}, {"response time", "Response time"},
}

// parseECM reads the "key: value" lines that oscam, ncam and cccam write to ecm.info.
func parseECM(text string) []infoPair {
	values := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "=") || strings.HasPrefix(line, "#") {
			continue
		}
		i := strings.Index(line, ":")
		if i <= 0 || i > 24 {
			continue
		}
		key := strings.ToLower(strings.TrimSpace(line[:i]))
		if _, seen := values[key]; !seen {
			values[key] = cleanText(strings.TrimSpace(line[i+1:]), 80)
		}
	}
	var out []infoPair
	shown := map[string]bool{}
	for _, e := range ecmLabels {
		v, ok := values[e.key]
		if !ok || v == "" || shown[e.label] {
			continue
		}
		shown[e.label] = true
		out = append(out, infoPair{e.label, v})
	}
	return out
}

func idText(v any, hex bool) string {
	text := jsonText(v)
	n, err := strconv.ParseInt(text, 10, 64)
	if err != nil || n <= 0 {
		if text == "0" || text == "N/A" || text == "-1" {
			return ""
		}
		return text
	}
	if hex {
		return fmt.Sprintf("0x%04X (%d)", n, n)
	}
	return text
}

func (s *Server) deviceServiceInfo(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 12*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	status, m, err := getJSONMap(ctx, web, "/api/getcurrent")
	if writeWebStatus(w, web, status, err) {
		return
	}
	info := asMap(m["info"])
	if info == nil {
		info = m
	}
	add := func(list *[]infoPair, label, value string) {
		if value != "" {
			*list = append(*list, infoPair{label, value})
		}
	}
	var service, ids []infoPair
	add(&service, "Channel", cleanText(jsonText(info["name"]), 120))
	add(&service, "Provider", cleanText(jsonText(info["provider"]), 120))
	add(&service, "Bouquet", cleanText(jsonText(info["bqname"]), 120))
	if w, h := idText(info["width"], false), idText(info["height"], false); w != "" && h != "" {
		add(&service, "Video size", w+" × "+h)
	}
	if wide, ok := info["iswidescreen"].(bool); ok {
		if wide {
			add(&service, "Aspect", "Widescreen")
		} else {
			add(&service, "Aspect", "4:3")
		}
	}
	add(&ids, "Video PID", idText(info["vpid"], true))
	add(&ids, "Audio PID", idText(info["apid"], true))
	add(&ids, "PCR PID", idText(info["pcrpid"], true))
	add(&ids, "PMT PID", idText(info["pmtpid"], true))
	add(&ids, "Teletext PID", idText(info["txtpid"], true))
	add(&ids, "SID", idText(info["sid"], true))
	add(&ids, "TSID", idText(info["tsid"], true))
	add(&ids, "ONID", idText(info["onid"], true))
	ref := jsonText(info["ref"])
	if !serviceRefPattern.MatchString(ref) {
		ref = ""
	}
	add(&ids, "Service reference", ref)

	var ecm []infoPair
	if status, body, err := web.getBounded(ctx, "/file?action=download&file="+url.QueryEscape("/tmp/ecm.info"), 8192); err == nil && status == http.StatusOK {
		if text := string(body); !strings.HasPrefix(text, "File '") {
			ecm = parseECM(text)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"service": service, "ids": ids, "ecm": ecm})
}

type gridEvent struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Short    string `json:"short,omitempty"`
	Begin    int64  `json:"begin"`
	Duration int64  `json:"duration"`
}

type gridChannel struct {
	Ref    string      `json:"ref"`
	Name   string      `json:"name"`
	Events []gridEvent `json:"events"`
}

const (
	maxGridSpan     = 12 * 3600
	maxGridChannels = 300
	maxGridPerChan  = 60
)

// deviceEPGGrid returns every channel of one bouquet with its programmes inside a time window.
func (s *Server) deviceEPGGrid(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	bouquet := strings.TrimSpace(query.Get("bouquet"))
	if !bouquetRefPattern.MatchString(bouquet) {
		writeError(w, http.StatusBadRequest, "not a bouquet reference")
		return
	}
	from, _ := strconv.ParseInt(query.Get("from"), 10, 64)
	to, _ := strconv.ParseInt(query.Get("to"), 10, 64)
	if from <= 0 {
		from = time.Now().Unix()
	}
	if to <= from || to-from > maxGridSpan {
		to = from + 3*3600
	}
	web := newDeviceWeb(profile, 30*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	path := "/api/epgbouquet?bRef=" + url.QueryEscape(bouquet) + "&time=" + strconv.FormatInt(from, 10) + "&endTime=" + strconv.FormatInt(to, 10)
	status, body, err := web.getBounded(ctx, path, maxChannelListBytes)
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
	events, now := guideEvents(raw.Events, 20000, "")
	index := map[string]int{}
	var channels []gridChannel
	for _, e := range events {
		if e.Begin+e.Duration <= from || e.Begin >= to {
			continue
		}
		i, seen := index[e.Ref]
		if !seen {
			if len(channels) >= maxGridChannels {
				continue
			}
			i = len(channels)
			index[e.Ref] = i
			channels = append(channels, gridChannel{Ref: e.Ref, Name: e.Channel})
		}
		if len(channels[i].Events) < maxGridPerChan {
			channels[i].Events = append(channels[i].Events, gridEvent{ID: e.ID, Title: e.Title, Short: e.Short, Begin: e.Begin, Duration: e.Duration})
		}
	}
	if now == 0 {
		now = time.Now().Unix()
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"now": now, "from": from, "to": to, "channels": channels})
}

// learnMAC remembers the receiver's MAC so Wake-on-LAN works after deep standby.
func (s *Server) learnMAC(profile config.Profile, about map[string]any) {
	if profile.MAC != "" {
		return
	}
	list, _ := asMap(about["info"])["ifaces"].([]any)
	for _, item := range list {
		mac := strings.ToLower(strings.TrimSpace(jsonText(asMap(item)["mac"])))
		if hw, err := net.ParseMAC(mac); err != nil || len(hw) != 6 || mac == "00:00:00:00:00:00" {
			continue
		}
		profile.MAC = mac
		if _, err := s.app.Profiles.Save(profile); err != nil {
			devLog.Warn("could not store the receiver's MAC address", "error", err)
		}
		return
	}
}

func (s *Server) deviceWake(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	hw, err := net.ParseMAC(profile.MAC)
	if err != nil || len(hw) != 6 {
		writeError(w, http.StatusConflict, "The receiver's MAC address is not known yet.")
		return
	}
	packet := make([]byte, 0, 102)
	for i := 0; i < 6; i++ {
		packet = append(packet, 0xff)
	}
	for i := 0; i < 16; i++ {
		packet = append(packet, hw...)
	}
	targets := []string{"255.255.255.255:9"}
	if ip := net.ParseIP(strings.Trim(profile.Host, "[]")); ip != nil && ip.To4() != nil {
		v4 := ip.To4()
		targets = append(targets, fmt.Sprintf("%d.%d.%d.255:9", v4[0], v4[1], v4[2]), net.JoinHostPort(v4.String(), "9"))
	}
	sent := 0
	for _, target := range targets {
		conn, err := net.DialTimeout("udp4", target, 2*time.Second)
		if err != nil {
			continue
		}
		if _, err := conn.Write(packet); err == nil {
			sent++
		}
		_ = conn.Close()
	}
	if sent == 0 {
		writeError(w, http.StatusBadGateway, "The wake-up packet could not be sent from this computer's network.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "mac": profile.MAC})
}
