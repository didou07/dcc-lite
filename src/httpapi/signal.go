package httpapi

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"
)

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
		out.AGC, _ = percent(raw["acg"])
	}
	out.BER, _ = percent(raw["ber"])
	out.SNRdB, out.HasDB = decibels(raw["snr_db"])
	out.Standby = jsonText(raw["inStandby"]) == "true"
	if n := jsonText(raw["tunernumber"]); n != "" {
		out.Tuner = joinWords(jsonText(raw["tunertype"]), "#"+n)
	}

	out.Locked = !out.Standby && out.SNR > 0
	return out
}

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

func (s *Server) currentChannelName(ctx context.Context, web deviceWeb) string {
	_, m, err := getJSONMap(ctx, web, "/api/getcurrent")
	if err != nil || m == nil {
		return ""
	}
	return jsonText(asMap(m["info"])["name"])
}
