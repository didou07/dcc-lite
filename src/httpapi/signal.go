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
	HasAGC    bool    `json:"has_agc"`
	BER       uint64  `json:"ber"`
	HasBER    bool    `json:"has_ber"`
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

func signalBool(values ...any) (bool, bool) {
	for _, value := range values {
		switch v := value.(type) {
		case bool:
			return v, true
		case float64:
			if v == 0 || v == 1 {
				return v == 1, true
			}
		case int:
			if v == 0 || v == 1 {
				return v == 1, true
			}
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true", "yes", "on", "1", "locked", "lock":
				return true, true
			case "false", "no", "off", "0", "unlocked", "unlock":
				return false, true
			}
		}
	}
	return false, false
}

func signalCounter(value any) (uint64, bool) {
	text := strings.TrimSpace(jsonText(value))
	if text == "" {
		return 0, false
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

func firstSignalValue(raw map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := raw[key]; ok && strings.TrimSpace(jsonText(value)) != "" {
			return value
		}
	}
	return nil
}

func buildLiveSignal(raw map[string]any) liveSignal {
	out := liveSignal{}
	snr, ok := percent(firstSignalValue(raw, "snr", "SNR"))
	if !ok {
		return out
	}
	out.Available = true
	out.SNR = snr
	out.AGC, out.HasAGC = percent(firstSignalValue(raw, "agc", "acg", "AGC"))
	out.BER, out.HasBER = signalCounter(firstSignalValue(raw, "ber", "BER"))
	out.SNRdB, out.HasDB = decibels(firstSignalValue(raw, "snr_db", "snrdb", "SNRdB"))
	out.Standby, _ = signalBool(raw["inStandby"], raw["in_standby"], raw["standby"])
	if n := jsonText(firstSignalValue(raw, "tunernumber", "tuner_number")); n != "" {
		out.Tuner = joinWords(jsonText(firstSignalValue(raw, "tunertype", "tuner_type")), "#"+n)
	}

	if locked, present := signalBool(raw["lock"], raw["locked"], raw["has_lock"]); present {
		out.Locked = locked && !out.Standby
	} else {
		out.Locked = !out.Standby && out.SNR > 0
	}
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
