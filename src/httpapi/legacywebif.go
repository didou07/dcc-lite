package httpapi

// Compatibility layer: answers OpenWebif /api/ requests from the old WebInterface (/web/, XML)
// only when the receiver has no such /api/ address. OpenWebif receivers never reach it.

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

// How long a receiver that lacked an /api/ address is trusted to still lack it. After that the
// /api/ address is tried once more (a receiver can get OpenWebif installed later).
const legacyMemory = 10 * time.Minute

var legacySeen sync.Map // device base + "|" + route name -> time.Time

func legacyKnown(base, route string) bool {
	v, ok := legacySeen.Load(base + "|" + route)
	if !ok {
		return false
	}
	if time.Since(v.(time.Time)) > legacyMemory {
		legacySeen.Delete(base + "|" + route)
		return false
	}
	return true
}

func legacyMark(base, route string, on bool) {
	if on {
		if !legacyKnown(base, route) {
			devLog.Info("receiver has no OpenWebif address, using the older web interface", "base", base, "route", route)
		}
		legacySeen.Store(base+"|"+route, time.Now())
		return
	}
	legacySeen.Delete(base + "|" + route)
}

func looksJSON(body []byte) bool {
	t := bytes.TrimLeft(body, " \t\r\n\xef\xbb\xbf")
	return len(t) > 0 && (t[0] == '{' || t[0] == '[')
}

// apiMissing reports whether a reply to an /api/ request means "this receiver does not have that
// address" (as opposed to an error, a wrong password or a working answer).
func apiMissing(status int, body []byte) bool {
	switch status {
	case http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusNotImplemented:
		return true
	case http.StatusOK:
		return !looksJSON(body)
	}
	return false
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
	for _, name := range []string{"epgnow", "epgservice", "epgsearch"} {
		web := "/web/" + name
		add("/api/"+name, func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
			return legacyEvents(ctx, d, web, q)
		})
	}
	add("/api/epgbouquet", func(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
		return legacyEvents(ctx, d, "/web/epgmulti", q)
	})
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

// viaLegacy is called around a request for an /api/ address. do makes the normal request.
func (d deviceWeb) viaLegacy(ctx context.Context, pathAndQuery string, do func() (int, []byte, error)) (int, []byte, error) {
	route, q := legacyRouteFor(pathAndQuery)
	if route == nil || d.kind == "openwebif" {
		return do() // chosen by the owner: never ask the older interface
	}
	if d.kind == "webinterface" {
		// chosen by the owner: no /api/ probing at all
		out, err := route.run(ctx, d, q)
		if err == nil {
			return http.StatusOK, out, nil
		}
		var bad *legacyBadReply
		if !errors.As(err, &bad) {
			return 0, nil, err
		}
		return do()
	}
	if legacyKnown(d.base, route.name) {
		out, err := route.run(ctx, d, q)
		if err == nil {
			return http.StatusOK, out, nil
		}
		var bad *legacyBadReply
		if !errors.As(err, &bad) {
			return 0, nil, err
		}
		legacyMark(d.base, route.name, false) // it stopped working: ask the /api/ address again
	}
	status, body, err := do()
	if err != nil || !apiMissing(status, body) {
		return status, body, err
	}
	out, lerr := route.run(ctx, d, q)
	if lerr == nil {
		legacyMark(d.base, route.name, true)
		return http.StatusOK, out, nil
	}
	var bad *legacyBadReply
	if !errors.As(lerr, &bad) {
		return 0, nil, lerr
	}
	devLog.Debug("older web interface could not answer either", "base", d.base, "route", route.name, "error", lerr)
	return status, body, nil
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
	case "true", "yes", "on", "1":
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
	legacyTVRoot    = `1:7:1:0:0:0:0:0:0:0:(type == 1) || (type == 17) || (type == 195) || (type == 25) FROM BOUQUET "bouquets.tv" ORDER BY bouquet`
	legacyRadioRoot = `1:7:2:0:0:0:0:0:0:0:(type == 2) FROM BOUQUET "bouquets.radio" ORDER BY bouquet`
	legacyMaxLists  = 120
)

// legacyServices answers /api/getallservices: every bouquet with its channels.
func legacyServices(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
	radio := q.Get("type") == "radio"
	var bouquets []xBouquet
	if !radio {
		// One request for everything, where the receiver offers it.
		var all struct {
			Bouquets []xBouquet `xml:"e2bouquet"`
		}
		if err := d.legacyXML(ctx, "/web/getallservices", maxChannelListBytes, &all); err == nil {
			for _, b := range all.Bouquets {
				if len(b.Services) > 0 {
					bouquets = all.Bouquets
					break
				}
			}
		} else {
			var bad *legacyBadReply
			if !errors.As(err, &bad) {
				return nil, err
			}
		}
	}
	if len(bouquets) == 0 {
		roots := []string{""}
		if radio {
			roots = []string{legacyRadioRoot}
		} else {
			roots = append(roots, legacyTVRoot)
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
		return nil, err
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
		State string `xml:"e2state"`
		Text  string `xml:"e2statetext"`
	}
	if err := d.legacyXML(ctx, withQuery(webPath, q), 64<<10, &res); err != nil {
		return nil, err
	}
	return asJSON(map[string]any{"result": xmlBool(res.State), "message": strings.TrimSpace(res.Text)})
}

// legacyAbout answers /api/about with what the old interface knows about the receiver.
func legacyAbout(ctx context.Context, d deviceWeb, _ url.Values) ([]byte, error) {
	var about struct {
		About struct {
			Device   string `xml:"e2devicename"`
			Image    string `xml:"e2imageversion"`
			Enigma   string `xml:"e2enigmaversion"`
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
			} `xml:"e2hddinfo"`
		} `xml:"e2about"`
	}
	if err := d.legacyXML(ctx, "/web/about", 1<<20, &about); err != nil {
		return nil, err
	}
	a := about.About
	if a.Device == "" && a.Image == "" && a.Enigma == "" {
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
	ifaces := []map[string]any{}
	for _, n := range a.Nics {
		if m := strings.TrimSpace(n.MAC); m != "" {
			ifaces = append(ifaces, map[string]any{"mac": m})
		}
	}
	hdd := []map[string]any{}
	for _, h := range a.Disks {
		model, capacity, free := first(h.ModelA, h.Model), first(h.CapA, h.Cap), first(h.FreeA, h.Free)
		if model == "" && capacity == "" {
			continue
		}
		text := capacity
		if free != "" {
			text = free + " free / " + capacity
		}
		hdd = append(hdd, map[string]any{"model": model, "friendlycapacity": text})
	}
	info := map[string]any{
		"model": strings.TrimSpace(a.Device), "imagever": strings.TrimSpace(a.Image),
		"enigmaver": strings.TrimSpace(a.Enigma), "tuners": tuners, "ifaces": ifaces, "hdd": hdd,
	}
	return asJSON(map[string]any{"result": true, "info": info})
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
