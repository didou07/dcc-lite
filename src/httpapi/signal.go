package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// liveSignal is one reading of the receiver's tuner, used by the Signal tab
// while someone turns the dish or moves the LNB.
type liveSignal struct {
	Available bool    `json:"available"`
	Locked    bool    `json:"locked"`
	SNR       int     `json:"snr"`
	SNRdB     float64 `json:"snr_db"`
	HasDB     bool    `json:"has_db"`
	AGC       int     `json:"agc"`
	BER       int     `json:"ber"`
	Tuner     string  `json:"tuner,omitempty"`
	Standby   bool    `json:"standby,omitempty"`
	Channel   string  `json:"channel,omitempty"`
}

// decibels reads a value such as "12.50", "12.5 dB" or 12.5.
func decibels(v any) (float64, bool) {
	t := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(strings.ToLower(jsonText(v))), "db"))
	if t == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || f < 0 || f > 100 {
		return 0, false
	}
	return f, true
}

// buildLiveSignal reads the answer of OpenWebif's "tunersignal" (or "signal")
// call. A receiver in standby, or without a lock, reports empty values.
func buildLiveSignal(raw map[string]any) liveSignal {
	out := liveSignal{}
	snr, ok := percent(raw["snr"])
	if !ok {
		return out
	}
	out.Available = true
	out.SNR = snr
	out.AGC, _ = percent(raw["agc"])
	if out.AGC == 0 {
		out.AGC, _ = percent(raw["acg"]) // older OpenWebif spells it "acg"
	}
	out.BER, _ = percent(raw["ber"])
	out.SNRdB, out.HasDB = decibels(raw["snr_db"])
	out.Standby = jsonText(raw["inStandby"]) == "true"
	if n := jsonText(raw["tunernumber"]); n != "" {
		out.Tuner = joinWords(jsonText(raw["tunertype"]), "#"+n)
	}
	// A tuner that is locked reports a real signal quality; no SNR at all means no lock.
	out.Locked = !out.Standby && out.SNR > 0
	return out
}

// deviceSignal returns the current tuner signal quality. The browser calls it
// a few times a second while the Signal tab is open, so it asks the receiver
// for the small "tunersignal" answer only; the channel name is read now and
// then (query "channel=1") rather than on every call.
func (s *Server) deviceSignal(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 4*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 4*time.Second)
	defer cancel()
	status, raw, err := getJSONMap(ctx, web, "/api/tunersignal")
	if err != nil || status != http.StatusOK {
		// Some images only have the older call.
		if status == http.StatusNotFound {
			status, raw, err = getJSONMap(ctx, web, "/api/signal")
		}
		if err != nil || status != http.StatusOK {
			if err == nil && status == http.StatusOK {
				writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return its signal.", "code": "web_error"})
				return
			}
			writeWebStatus(w, web, status, err)
			return
		}
	}
	signal := buildLiveSignal(raw)
	if r.URL.Query().Get("channel") == "1" {
		signal.Channel = s.currentChannelName(ctx, web)
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, signal)
}

// currentChannelName is the name of the channel the receiver is tuned to, or "".
func (s *Server) currentChannelName(ctx context.Context, web deviceWeb) string {
	_, m, err := getJSONMap(ctx, web, "/api/getcurrent")
	if err != nil || m == nil {
		return ""
	}
	return jsonText(asMap(m["info"])["name"])
}
