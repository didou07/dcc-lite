package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

var legacyWarned sync.Map

// legacyWarnDue limits the same warning to once a minute so polling does not fill the log.
func legacyWarnDue(base, route string) bool {
	key := base + "|" + route
	if v, ok := legacyWarned.Load(key); ok && time.Since(v.(time.Time)) < time.Minute {
		return false
	}
	legacyWarned.Store(key, time.Now())
	return true
}

func looksJSON(body []byte) bool {
	t := bytes.TrimLeft(body, " \t\r\n\xef\xbb\xbf")
	return len(t) > 0 && (t[0] == '{' || t[0] == '[')
}

type legacyBadReply struct{ msg string }

func (e *legacyBadReply) Error() string { return e.msg }

type legacyRoute struct {
	name string
	run  func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error)
}

var legacyRoutes map[string]*legacyRoute

func init() {
	legacyRoutes = map[string]*legacyRoute{}
	add := func(path string, run func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error)) {
		legacyRoutes[path] = &legacyRoute{name: strings.TrimPrefix(path, "/api/"), run: run}
	}
	add("/api/getallservices", legacyServices)
	add("/api/getcurrent", legacyCurrent)
	add("/api/powerstate", legacyPower)
	add("/api/about", legacyAbout)
	add("/api/tunersignal", legacySignal)
	add("/api/timerlist", legacyTimers)
	add("/api/movielist", legacyMovies)
	add("/api/epgnow", func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
		return legacyEvents(ctx, d, "/web/epgnownext", q)
	})
	add("/api/epgsimilar", func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
		return legacyEvents(ctx, d, "/web/epgsimilar", q)
	})
	add("/api/signal", legacySignal)
	add("/api/getaudiotracks", legacyAudioTracks)
	add("/api/recordnow", func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
		// OpenWebif says infinite=1; the old interface says recordnow=infinite|current.
		nq := url.Values{}
		if v := q.Get("infinite"); v == "1" || strings.EqualFold(v, "true") {
			nq.Set("recordnow", "infinite")
		} else {
			nq.Set("recordnow", "current")
		}
		return legacyResult(ctx, d, "/web/recordnow", nq)
	})
	for _, name := range []string{"epgservice", "epgsearch"} {
		web := "/web/" + name
		add("/api/"+name, func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
			return legacyEvents(ctx, d, web, q)
		})
	}
	add("/api/epgbouquet", func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
		return legacyEvents(ctx, d, "/web/epgmulti", q)
	})
	// Documented DreamOS twins whose name or answer differs only slightly.
	add("/api/timertogglestatus", legacyTimerToggle)
	for _, name := range []string{"timerchange", "selectaudiotrack"} {
		web := "/web/" + name
		add("/api/"+name, func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
			return legacyResult(ctx, d, web, q)
		})
	}
	add("/api/message", func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
		return legacyResult(ctx, d, "/web/message", q)
	})
	add("/api/sleeptimer", legacySleepTimer)
	add("/api/getlocations", legacyLocations)
	add("/api/moviemove", func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
		return legacyResult(ctx, d, "/web/moviemove", q)
	})
	add("/api/currenttime", legacyCurrentTime)
	for _, name := range []string{"servicelistreload", "timeradd", "timeraddbyeventid", "timerdelete", "timercleanup", "moviedelete"} {
		web := "/web/" + name
		add("/api/"+name, func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
			return legacyResult(ctx, d, web, q)
		})
	}
}

func legacyRouteFor(pathAndQuery string) (*legacyRoute, url.Values) {
	path, rawQuery, _ := strings.Cut(pathAndQuery, "?")
	route := legacyRoutes[path]
	if route == nil {
		return nil, nil
	}
	q, _ := url.ParseQuery(rawQuery)
	return route, q
}

func (d deviceWeb) viaLegacy(ctx context.Context, pathAndQuery string, do func() (int, []byte, error)) (int, []byte, error) {
	if d.kind != "dreamos" || !strings.HasPrefix(pathAndQuery, "/api/") {
		return do()
	}
	route, q := legacyRouteFor(pathAndQuery)
	if route == nil {
		return 0, nil, &webUnsupportedError{strings.TrimPrefix(pathOnly(pathAndQuery), "/api/")}
	}
	out, err := route.run(ctx, d, q)
	if err == nil {
		return http.StatusOK, out, nil
	}
	var bad *legacyBadReply
	if !errors.As(err, &bad) {
		return 0, nil, err
	}
	if legacyWarnDue(d.base, route.name) {
		devLog.Warn("DreamOS could not answer", "base", d.base, "route", route.name, "reason", bad.msg)
	}
	return 0, nil, bad
}

// ---- Reading the old interface ----
func xmlClean(body []byte) []byte {
	out := bytes.Map(func(r rune) rune {
		if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
			return ' '
		}
		return r
	}, body)
	return []byte(strings.ToValidUTF8(string(out), "?"))
}

// legacyXML fetches an old /web/ address and decodes its XML into v.
func (d deviceWeb) legacyXML(ctx context.Context, pathAndQuery string, limit int64, v any) error {
	status, body, err := d.getBoundedRaw(ctx, pathAndQuery, limit)
	if err != nil {
		return err
	}
	if status != http.StatusOK {
		return &legacyBadReply{fmt.Sprintf("HTTP %d from %s", status, pathOnly(pathAndQuery))}
	}
	dec := xml.NewDecoder(bytes.NewReader(xmlClean(body)))
	dec.Strict = false
	dec.CharsetReader = func(_ string, in io.Reader) (io.Reader, error) { return in, nil }
	if err := dec.Decode(v); err != nil {
		return &legacyBadReply{"not XML from " + pathOnly(pathAndQuery) + ": " + err.Error()}
	}
	return nil
}

func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

func num(s string) int64 {
	s = strings.TrimSpace(s)
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return int64(f)
	}
	return 0
}

func xmlBool(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "on", "1", "success":
		return true
	}
	return false
}

func asJSON(v any) ([]byte, error) { return json.Marshal(v) }

type xSvc struct {
	Ref  string `xml:"e2servicereference"`
	Name string `xml:"e2servicename"`
}

type xBouquet struct {
	Ref      string `xml:"e2servicereference"`
	Name     string `xml:"e2servicename"`
	Services []xSvc `xml:"e2servicelist>e2service"`
}

const (
	legacyTVRoot    = `1:7:1:0:0:0:0:0:0:0:(type == 1) || (type == 17) || (type == 22) || (type == 25) || (type == 31) || (type == 134) || (type == 195) FROM BOUQUET "bouquets.tv" ORDER BY bouquet`
	legacyRadioRoot = `1:7:2:0:0:0:0:0:0:0:(type == 2) || (type == 10) FROM BOUQUET "bouquets.radio" ORDER BY bouquet`
	legacyMaxLists  = 256
)

// legacyServices answers /api/getallservices: every bouquet with its channels.
func legacyServices(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
	radio := q.Get("type") == "radio"
	var bouquets []xBouquet
	roots := []string{legacyTVRoot, ""}
	if radio {
		roots = []string{legacyRadioRoot, ""}
	}
	var heads []xSvc
	for _, root := range roots {
		path := "/web/getservices"
		if root != "" {
			path += "?sRef=" + url.QueryEscape(root)
		}
		var list struct {
			Services []xSvc `xml:"e2service"`
		}
		if err := d.legacyXML(ctx, path, maxChannelListBytes, &list); err != nil {
			var bad *legacyBadReply
			if !errors.As(err, &bad) {
				return nil, err
			}
			continue
		}
		if len(list.Services) > 0 {
			heads = list.Services
			break
		}
	}
	if len(heads) == 0 {
		return nil, &legacyBadReply{"the receiver listed no bouquets"}
	}
	if len(heads) > legacyMaxLists {
		devLog.Warn("DreamOS bouquet list cut", "base", d.base, "bouquets", len(heads), "kept", legacyMaxLists)
		heads = heads[:legacyMaxLists]
	}
	for _, h := range heads {
		ref := strings.TrimSpace(h.Ref)
		if ref == "" {
			continue
		}
		var list struct {
			Services []xSvc `xml:"e2service"`
		}
		if err := d.legacyXML(ctx, "/web/getservices?sRef="+url.QueryEscape(ref), maxChannelListBytes, &list); err != nil {
			var bad *legacyBadReply
			if !errors.As(err, &bad) {
				return nil, err
			}
			continue
		}
		bouquets = append(bouquets, xBouquet{Ref: ref, Name: h.Name, Services: list.Services})
	}
	type sub struct {
		Ref  string `json:"servicereference"`
		Name string `json:"servicename"`
		Pos  int    `json:"pos"`
	}
	type bq struct {
		Ref  string `json:"servicereference"`
		Name string `json:"servicename"`
		Sub  []sub  `json:"subservices"`
	}
	out := make([]bq, 0, len(bouquets))
	for _, b := range bouquets {
		item := bq{Ref: strings.TrimSpace(b.Ref), Name: strings.TrimSpace(b.Name), Sub: []sub{}}
		pos := 0
		for _, s := range b.Services {
			ref := strings.TrimSpace(s.Ref)
			if ref == "" {
				continue
			}
			entry := sub{Ref: ref, Name: strings.TrimSpace(s.Name)}
			if serviceFlags(ref)&64 == 0 {
				pos++
				entry.Pos = pos
			}
			item.Sub = append(item.Sub, entry)
		}
		out = append(out, item)
	}
	return asJSON(map[string]any{"result": true, "services": out})
}

type xEvent struct {
	ID       string `xml:"e2eventid"`
	Start    string `xml:"e2eventstart"`
	Duration string `xml:"e2eventduration"`
	Current  string `xml:"e2eventcurrenttime"`
	Title    string `xml:"e2eventtitle"`
	Short    string `xml:"e2eventdescription"`
	Long     string `xml:"e2eventdescriptionextended"`
	Ref      string `xml:"e2eventservicereference"`
	Name     string `xml:"e2eventservicename"`
}

// noTitle is what the old interface sends for "no programme information".
func noTitle(t string) bool {
	t = strings.TrimSpace(t)
	return t == "" || strings.EqualFold(t, "none") || t == "N/A"
}

// legacyEvents answers the programme guide requests (now, one channel, search, grid).
func legacyEvents(ctx context.Context, d deviceWeb, webPath string, q url.Values) ([]byte, error) {
	var list struct {
		Events []xEvent `xml:"e2event"`
	}
	if err := d.legacyXML(ctx, withQuery(webPath, q), maxChannelListBytes, &list); err != nil {
		return nil, err
	}
	type ev struct {
		ID       int64  `json:"id"`
		Ref      string `json:"sref"`
		Name     string `json:"sname"`
		Title    string `json:"title"`
		Short    string `json:"shortdesc"`
		Long     string `json:"longdesc"`
		Begin    int64  `json:"begin_timestamp"`
		Duration int64  `json:"duration_sec"`
		Now      int64  `json:"now_timestamp"`
	}
	out := make([]ev, 0, len(list.Events))
	for _, e := range list.Events {
		if noTitle(e.Title) {
			continue
		}
		out = append(out, ev{
			ID: num(e.ID), Ref: strings.TrimSpace(e.Ref), Name: strings.TrimSpace(e.Name), Title: strings.TrimSpace(e.Title),
			Short: e.Short, Long: e.Long, Begin: num(e.Start), Duration: num(e.Duration), Now: num(e.Current),
		})
	}
	return asJSON(map[string]any{"result": true, "events": out})
}

// legacyCurrent answers /api/getcurrent: the channel being watched and its now/next programme.
func legacyCurrent(ctx context.Context, d deviceWeb, _ url.Values) ([]byte, error) {
	var cur struct {
		Service struct {
			Ref      string `xml:"e2servicereference"`
			Name     string `xml:"e2servicename"`
			Provider string `xml:"e2providername"`
			Width    string `xml:"e2videowidth"`
			Height   string `xml:"e2videoheight"`
			Wide     string `xml:"e2iswidescreen"`
			Apid     string `xml:"e2apid"`
			Vpid     string `xml:"e2vpid"`
			Pcrpid   string `xml:"e2pcrpid"`
			Pmtpid   string `xml:"e2pmtpid"`
			Txtpid   string `xml:"e2txtpid"`
			Tsid     string `xml:"e2tsid"`
			Onid     string `xml:"e2onid"`
			Sid      string `xml:"e2sid"`
		} `xml:"e2service"`
		Events []xEvent `xml:"e2eventlist>e2event"`
	}
	if err := d.legacyXML(ctx, "/web/getcurrent", 1<<20, &cur); err != nil {
		var bad *legacyBadReply
		if !errors.As(err, &bad) {
			return nil, err
		}
		// Some images (e.g. DreamOS) lack /web/getcurrent; /web/about also names the current channel.
		var about struct {
			Service struct {
				Ref      string `xml:"e2servicereference"`
				Name     string `xml:"e2servicename"`
				Provider string `xml:"e2providername"`
				Width    string `xml:"e2videowidth"`
				Height   string `xml:"e2videoheight"`
				Wide     string `xml:"e2iswidescreen"`
				Apid     string `xml:"e2apid"`
				Vpid     string `xml:"e2vpid"`
				Pcrpid   string `xml:"e2pcrpid"`
				Pmtpid   string `xml:"e2pmtpid"`
				Txtpid   string `xml:"e2txtpid"`
				Tsid     string `xml:"e2tsid"`
				Onid     string `xml:"e2onid"`
				Sid      string `xml:"e2sid"`
			} `xml:"e2about>e2service"`
		}
		if aerr := d.legacyXML(ctx, "/web/about", 1<<20, &about); aerr != nil || (strings.TrimSpace(about.Service.Ref) == "" && strings.TrimSpace(about.Service.Name) == "") {
			return nil, err // report the first failure: it names the address that is missing
		}
		cur.Service = about.Service
	}
	s := cur.Service
	info := map[string]any{"ref": strings.TrimSpace(s.Ref), "name": strings.TrimSpace(s.Name)}
	text := func(key, v string) {
		if v = strings.TrimSpace(v); v != "" {
			info[key] = v
		}
	}
	text("provider", s.Provider)
	text("width", s.Width)
	text("height", s.Height)
	text("vpid", s.Vpid)
	text("apid", s.Apid)
	text("pcrpid", s.Pcrpid)
	text("pmtpid", s.Pmtpid)
	text("txtpid", s.Txtpid)
	text("tsid", s.Tsid)
	text("onid", s.Onid)
	text("sid", s.Sid)
	if w := strings.TrimSpace(s.Wide); w != "" {
		info["iswidescreen"] = xmlBool(w)
	}
	if info["ref"] == "" && len(cur.Events) > 0 {
		info["ref"] = strings.TrimSpace(cur.Events[0].Ref)
		info["name"] = strings.TrimSpace(cur.Events[0].Name)
	}
	event := func(i int) map[string]any {
		if i >= len(cur.Events) || noTitle(cur.Events[i].Title) {
			return map[string]any{"title": ""}
		}
		e := cur.Events[i]
		start, dur := num(e.Start), num(e.Duration)
		now := num(e.Current)
		if now <= 0 {
			now = time.Now().Unix()
		}
		remaining := int64(0)
		if i == 0 && start > 0 && start+dur > now {
			remaining = start + dur - now
		}
		return map[string]any{"title": strings.TrimSpace(e.Title), "begin_timestamp": start, "duration_sec": dur, "remaining": remaining}
	}
	return asJSON(map[string]any{"result": true, "info": info, "now": event(0), "next": event(1)})
}

// legacyPower answers /api/powerstate, with or without newstate.
func legacyPower(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
	keep := url.Values{}
	if v := q.Get("newstate"); v != "" {
		keep.Set("newstate", v)
	}
	var st struct {
		Standby string `xml:"e2instandby"`
	}
	if err := d.legacyXML(ctx, withQuery("/web/powerstate", keep), 64<<10, &st); err != nil {
		return nil, err
	}
	return asJSON(map[string]any{"result": true, "instandby": xmlBool(st.Standby)})
}

// legacyResult answers the commands that only say whether they worked (<e2simplexmlresult>).
func legacyResult(ctx context.Context, d deviceWeb, webPath string, q url.Values) ([]byte, error) {
	var res struct {
		State      string `xml:"e2state"`
		Text       string `xml:"e2statetext"`
		Result     string `xml:"e2result"`
		ResultText string `xml:"e2resulttext"`
		Bare       string `xml:",chardata"`
	}
	if err := d.legacyXML(ctx, withQuery(webPath, q), 64<<10, &res); err != nil {
		return nil, err
	}
	ok, text := false, strings.TrimSpace(res.Text)
	switch {
	case strings.TrimSpace(res.State) != "":
		ok = xmlBool(res.State)
	case strings.TrimSpace(res.Result) != "":
		ok, text = xmlBool(res.Result), strings.TrimSpace(res.ResultText)
	default:
		ok = xmlBool(res.Bare)
	}
	return asJSON(map[string]any{"result": ok, "message": text})
}

// legacyAbout answers /api/about with what the old interface knows about the receiver.
type legacyAboutData struct {
	Device string `xml:"e2devicename"`
	Model  string `xml:"e2model"` // /web/about of the official DreamOS plugin
	Nim    []struct {
		Name string `xml:"name"`
		Type string `xml:"type"`
	} `xml:"e2tunerinfo>e2nim"`
	Image    string `xml:"e2imageversion"`
	Enigma   string `xml:"e2enigmaversion"`
	WebIf    string `xml:"e2webifversion"`
	Frontend []struct {
		Name  string `xml:"e2name"`
		Model string `xml:"e2model"`
	} `xml:"e2frontends>e2frontend"`
	Nics []struct {
		MAC string `xml:"e2mac"`
	} `xml:"e2network>e2interface"`
	Disks []struct {
		ModelA string `xml:"model,attr"`
		CapA   string `xml:"capacity,attr"`
		FreeA  string `xml:"free,attr"`
		Model  string `xml:"e2model"`
		Cap    string `xml:"e2capacity"`
		Free   string `xml:"e2free"`
		CModel string `xml:"model"`
		CCap   string `xml:"capacity"`
		CFree  string `xml:"free"`
	} `xml:"e2hddinfo"`
	Hdds []struct {
		Model string `xml:"e2model"`
		Cap   string `xml:"e2capacity"`
		Free  string `xml:"e2free"`
	} `xml:"e2hdds>e2hdd"`
}

func (a legacyAboutData) empty() bool {
	return a.Device == "" && a.Model == "" && a.Image == "" && a.Enigma == ""
}

func legacyAbout(ctx context.Context, d deviceWeb, _ url.Values) ([]byte, error) {
	var a legacyAboutData
	var info struct {
		legacyAboutData
	}
	var derr error
	if legacyRecall(d.base, "about") != "/web/about" {
		derr = d.legacyXML(ctx, "/web/deviceinfo", 1<<20, &info)
	}
	if derr == nil && !info.legacyAboutData.empty() {
		a = info.legacyAboutData
		legacyRemember(d.base, "about", "/web/deviceinfo")
	} else {
		var about struct {
			About legacyAboutData `xml:"e2about"`
		}
		if err := d.legacyXML(ctx, "/web/about", 1<<20, &about); err != nil {
			if derr != nil {
				return nil, derr // the receiver's answer to the address it is expected to have
			}
			return nil, err
		}
		a = about.About
		if !a.empty() {
			legacyRemember(d.base, "about", "/web/about")
		}
	}
	if a.empty() {
		return nil, &legacyBadReply{"the receiver described nothing about itself"}
	}
	first := func(a, b string) string {
		if strings.TrimSpace(a) != "" {
			return strings.TrimSpace(a)
		}
		return strings.TrimSpace(b)
	}
	tuners := []map[string]any{}
	for _, f := range a.Frontend {
		if name := first(f.Name, ""); name != "" {
			label := name
			if m := strings.TrimSpace(f.Model); m != "" {
				label += ": " + m
			}
			tuners = append(tuners, map[string]any{"name": label})
		}
	}
	for _, n := range a.Nim {
		if name := strings.TrimSpace(n.Name); name != "" {
			label := name
			if t := strings.TrimSpace(n.Type); t != "" {
				label += ": " + t
			}
			tuners = append(tuners, map[string]any{"name": label})
		}
	}
	ifaces := []map[string]any{}
	for _, n := range a.Nics {
		if m := strings.TrimSpace(n.MAC); m != "" {
			ifaces = append(ifaces, map[string]any{"mac": m})
		}
	}
	hdd := []map[string]any{}
	addDisk := func(model, capacity, free string) {
		if model == "" && capacity == "" {
			return
		}
		text := capacity
		if free != "" {
			text = free + " free / " + capacity
		}
		hdd = append(hdd, map[string]any{"model": model, "friendlycapacity": text})
	}
	for _, h := range a.Disks {
		addDisk(first(h.ModelA, first(h.Model, h.CModel)), first(h.CapA, first(h.Cap, h.CCap)), first(h.FreeA, first(h.Free, h.CFree)))
	}
	for _, h := range a.Hdds {
		addDisk(strings.TrimSpace(h.Model), strings.TrimSpace(h.Cap), strings.TrimSpace(h.Free))
	}
	out := map[string]any{
		"model": first(a.Device, a.Model), "imagever": strings.TrimSpace(a.Image),
		"enigmaver": strings.TrimSpace(a.Enigma), "tuners": tuners, "ifaces": ifaces, "hdd": hdd,
	}
	if v := strings.TrimSpace(a.WebIf); v != "" {
		out["webifver"] = v
	}
	return asJSON(map[string]any{"result": true, "info": out})
}

func legacyCurrentTime(ctx context.Context, d deviceWeb, _ url.Values) ([]byte, error) {
	var ct struct {
		Time string `xml:",chardata"`
	}
	if err := d.legacyXML(ctx, "/web/currenttime", 4<<10, &ct); err != nil {
		return nil, err
	}
	t := strings.TrimSpace(ct.Time)
	if t == "" {
		return nil, &legacyBadReply{"the receiver gave no time"}
	}
	return asJSON(map[string]any{"result": true, "time": t})
}

// legacySignal answers /api/tunersignal.
func legacySignal(ctx context.Context, d deviceWeb, _ url.Values) ([]byte, error) {
	var st struct {
		SNRdB string `xml:"e2snrdb"`
		SNR   string `xml:"e2snr"`
		BER   string `xml:"e2ber"`
		AGC   string `xml:"e2acg"`
		AGC2  string `xml:"e2agc"`
	}
	if err := d.legacyXML(ctx, "/web/signal", 64<<10, &st); err != nil {
		return nil, err
	}
	if strings.TrimSpace(st.SNR) == "" {
		return nil, &legacyBadReply{"the receiver reported no signal"}
	}
	agc := st.AGC
	if strings.TrimSpace(agc) == "" {
		agc = st.AGC2
	}
	out := map[string]any{"snr": strings.TrimSpace(st.SNR), "snr_db": strings.TrimSpace(st.SNRdB), "agc": strings.TrimSpace(agc), "ber": strings.TrimSpace(st.BER)}
	return asJSON(out)
}

// legacyTimers answers /api/timerlist.
func legacyTimers(ctx context.Context, d deviceWeb, _ url.Values) ([]byte, error) {
	var list struct {
		Timers []struct {
			Ref      string `xml:"e2servicereference"`
			Channel  string `xml:"e2servicename"`
			EIT      string `xml:"e2eit"`
			Name     string `xml:"e2name"`
			Desc     string `xml:"e2description"`
			Disabled string `xml:"e2disabled"`
			Begin    string `xml:"e2timebegin"`
			End      string `xml:"e2timeend"`
			JustPlay string `xml:"e2justplay"`
			After    string `xml:"e2afterevent"`
			File     string `xml:"e2filename"`
			State    string `xml:"e2state"`
			Repeated string `xml:"e2repeated"`
		} `xml:"e2timer"`
	}
	if err := d.legacyXML(ctx, "/web/timerlist", maxGuideBytes, &list); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(list.Timers))
	for _, t := range list.Timers {
		out = append(out, map[string]any{
			"serviceref": strings.TrimSpace(t.Ref), "servicename": strings.TrimSpace(t.Channel), "name": strings.TrimSpace(t.Name),
			"description": strings.TrimSpace(t.Desc), "begin": num(t.Begin), "end": num(t.End), "eit": num(t.EIT),
			"disabled": num(t.Disabled), "justplay": num(t.JustPlay), "state": num(t.State), "repeated": num(t.Repeated),
			"afterevent": num(t.After), "filename": strings.TrimSpace(t.File),
		})
	}
	return asJSON(map[string]any{"result": true, "timers": out})
}

func legacyTimerToggle(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
	var list struct {
		Timers []struct {
			Ref      string `xml:"e2servicereference"`
			EIT      string `xml:"e2eit"`
			Name     string `xml:"e2name"`
			Desc     string `xml:"e2description"`
			Disabled string `xml:"e2disabled"`
			Begin    string `xml:"e2timebegin"`
			End      string `xml:"e2timeend"`
			JustPlay string `xml:"e2justplay"`
			After    string `xml:"e2afterevent"`
			Repeated string `xml:"e2repeated"`
		} `xml:"e2timer"`
	}
	if err := d.legacyXML(ctx, "/web/timerlist", maxGuideBytes, &list); err != nil {
		return nil, err
	}
	ref, begin, end := strings.TrimSpace(q.Get("sRef")), num(q.Get("begin")), num(q.Get("end"))
	for _, t := range list.Timers {
		if !strings.EqualFold(strings.TrimSpace(t.Ref), ref) || num(t.Begin) != begin || num(t.End) != end {
			continue
		}
		disabled := "1"
		if num(t.Disabled) != 0 {
			disabled = "0"
		}
		nq := url.Values{}
		nq.Set("sRef", strings.TrimSpace(t.Ref))
		nq.Set("begin", strconv.FormatInt(begin, 10))
		nq.Set("end", strconv.FormatInt(end, 10))
		nq.Set("name", strings.TrimSpace(t.Name))
		nq.Set("description", strings.TrimSpace(t.Desc))
		nq.Set("eit", strings.TrimSpace(t.EIT))
		nq.Set("disabled", disabled)
		nq.Set("justplay", strconv.FormatInt(num(t.JustPlay), 10))
		nq.Set("afterevent", strconv.FormatInt(num(t.After), 10))
		nq.Set("repeated", strconv.FormatInt(num(t.Repeated), 10))
		nq.Set("channelOld", strings.TrimSpace(t.Ref))
		nq.Set("beginOld", strconv.FormatInt(begin, 10))
		nq.Set("endOld", strconv.FormatInt(end, 10))
		nq.Set("deleteOldOnSave", "1")
		return legacyResult(ctx, d, "/web/timerchange", nq)
	}
	return nil, &legacyBadReply{"the timer was not found in the receiver's timer list"}
}

// legacyMovies answers /api/movielist.
func legacyMovies(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
	keep := url.Values{}
	if v := q.Get("dirname"); v != "" {
		keep.Set("dirname", v)
	}
	var list struct {
		Movies []struct {
			Ref     string `xml:"e2servicereference"`
			Title   string `xml:"e2title"`
			Desc    string `xml:"e2description"`
			Ext     string `xml:"e2descriptionextended"`
			Channel string `xml:"e2servicename"`
			Time    string `xml:"e2time"`
			Length  string `xml:"e2length"`
			Tags    string `xml:"e2tags"`
			File    string `xml:"e2filename"`
			Size    string `xml:"e2filesize"`
		} `xml:"e2movie"`
	}
	if err := d.legacyXML(ctx, withQuery("/web/movielist", keep), maxChannelListBytes, &list); err != nil {
		return nil, err
	}
	movies := make([]map[string]any, 0, len(list.Movies))
	for _, m := range list.Movies {
		movies = append(movies, map[string]any{
			"serviceref": strings.TrimSpace(m.Ref), "filename": strings.TrimSpace(m.File), "eventname": strings.TrimSpace(m.Title),
			"servicename": strings.TrimSpace(m.Channel), "description": m.Desc, "descriptionExtended": m.Ext,
			"recordingtime": num(m.Time), "length": strings.TrimSpace(m.Length), "filesize": num(m.Size), "tags": strings.TrimSpace(m.Tags),
		})
	}
	return asJSON(map[string]any{"result": true, "directory": keep.Get("dirname"), "bookmarks": []string{}, "movies": movies})
}

// legacySleepTimer answers /api/sleeptimer from /web/sleeptimer (<e2sleeptimer>).
func legacySleepTimer(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
	var st struct {
		Enabled string `xml:"e2enabled"`
		Minutes string `xml:"e2minutes"`
		Action  string `xml:"e2action"`
		Text    string `xml:"e2text"`
	}
	if err := d.legacyXML(ctx, withQuery("/web/sleeptimer", q), 64<<10, &st); err != nil {
		return nil, err
	}
	if strings.TrimSpace(st.Enabled) == "" && strings.TrimSpace(st.Minutes) == "" {
		return nil, &legacyBadReply{"the receiver gave no sleep timer state"}
	}
	return asJSON(map[string]any{
		"enabled": xmlBool(st.Enabled), "minutes": num(st.Minutes),
		"action": strings.TrimSpace(st.Action), "message": strings.TrimSpace(st.Text),
	})
}

// legacyLocations answers /api/getlocations from /web/getlocations (<e2locations><e2location>).
func legacyLocations(ctx context.Context, d deviceWeb, _ url.Values) ([]byte, error) {
	var l struct {
		Locations []string `xml:"e2location"`
	}
	if err := d.legacyXML(ctx, "/web/getlocations", 256<<10, &l); err != nil {
		return nil, err
	}
	out := []string{}
	for _, v := range l.Locations {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return asJSON(map[string]any{"result": true, "locations": out})
}

func legacyAudioTracks(ctx context.Context, d deviceWeb, _ url.Values) ([]byte, error) {
	var l struct {
		Tracks []struct {
			Description string `xml:"e2audiotrackdescription"`
			ID          string `xml:"e2audiotrackid"`
			PID         string `xml:"e2audiotrackpid"`
			Active      string `xml:"e2audiotrackactive"`
		} `xml:"e2audiotrack"`
	}
	if err := d.legacyXML(ctx, "/web/getaudiotracks", 256<<10, &l); err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(l.Tracks))
	for i, t := range l.Tracks {
		idx := i
		if strings.TrimSpace(t.ID) != "" {
			idx = int(num(t.ID))
		}
		out = append(out, map[string]any{
			"index": idx, "description": strings.TrimSpace(t.Description),
			"pid": num(t.PID), "active": xmlBool(t.Active),
		})
	}
	return asJSON(map[string]any{"result": true, "tracklist": out})
}
