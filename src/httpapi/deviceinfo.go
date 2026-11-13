package httpapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type infoTuner struct {
	Name   string `json:"name"`
	Type   string `json:"type,omitempty"`
	Live   string `json:"live,omitempty"`
	Rec    string `json:"rec,omitempty"`
	Stream string `json:"stream,omitempty"`
}

type infoDisk struct {
	Name string `json:"name"`
	Text string `json:"text"`
}

type infoSignal struct {
	Tuner   string `json:"tuner,omitempty"`
	SNR     int    `json:"snr"`
	SNRdB   string `json:"snr_db,omitempty"`
	AGC     int    `json:"agc"`
	HasAGC  bool   `json:"has_agc"`
	BER     uint64 `json:"ber"`
	HasBER  bool   `json:"has_ber"`
	Standby bool   `json:"standby,omitempty"`
}

type deviceInfo struct {
	Model       string      `json:"model,omitempty"`
	Image       string      `json:"image,omitempty"`
	Enigma      string      `json:"enigma,omitempty"`
	Kernel      string      `json:"kernel,omitempty"`
	Chipset     string      `json:"chipset,omitempty"`
	Uptime      string      `json:"uptime,omitempty"`
	Memory      string      `json:"memory,omitempty"`
	Tuners      []infoTuner `json:"tuners"`
	Disks       []infoDisk  `json:"disks"`
	Signal      *infoSignal `json:"signal,omitempty"`
	Temperature *int        `json:"temperature,omitempty"`
}

func jsonText(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int32:
		return strconv.FormatInt(int64(x), 10)
	case int64:
		return strconv.FormatInt(x, 10)
	case uint:
		return strconv.FormatUint(uint64(x), 10)
	case uint32:
		return strconv.FormatUint(uint64(x), 10)
	case uint64:
		return strconv.FormatUint(x, 10)
	case bool:
		return strconv.FormatBool(x)
	}
	return ""
}

func asMap(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func joinWords(parts ...string) string {
	var out []string
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return strings.Join(out, " ")
}

func percent(v any) (int, bool) {
	t := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(jsonText(v)), "%"))
	if t == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(t, 64)
	if err != nil || f < 0 {
		return 0, false
	}
	if f > 100 {
		f = 100
	}
	return int(f + 0.5), true
}

func signalStandby(signal map[string]any) bool {
	standby, _ := signalBool(signal["inStandby"], signal["in_standby"], signal["standby"])
	return standby
}

func buildDeviceInfo(about, signal map[string]any) deviceInfo {
	info := asMap(about["info"])
	out := deviceInfo{Tuners: []infoTuner{}, Disks: []infoDisk{}}
	out.Model = joinWords(jsonText(info["brand"]), jsonText(info["model"]))
	distro := jsonText(info["friendlyimagedistro"])
	if distro == "" {
		distro = jsonText(info["imagedistro"])
	}
	out.Image = joinWords(distro, jsonText(info["imagever"]))
	out.Enigma = jsonText(info["enigmaver"])
	out.Kernel = jsonText(info["kernelver"])
	out.Chipset = jsonText(info["friendlychipsettext"])
	out.Uptime = jsonText(info["uptime"])
	out.Memory = jsonText(info["mem3"])
	if list, ok := info["tuners"].([]any); ok {
		for _, item := range list {
			t := asMap(item)
			if name := jsonText(t["name"]); name != "" {
				out.Tuners = append(out.Tuners, infoTuner{Name: name, Type: jsonText(t["type"]), Live: jsonText(t["live"]), Rec: jsonText(t["rec"]), Stream: jsonText(t["stream"])})
			}
		}
	}
	if list, ok := info["hdd"].([]any); ok {
		for _, item := range list {
			d := asMap(item)
			name := joinWords(jsonText(d["model"]), jsonText(d["mount"]))
			textValue := jsonText(d["friendlycapacity"])
			if textValue == "" {
				textValue = joinWords(jsonText(d["free"]), "free /", jsonText(d["capacity"]))
			}
			if name != "" || textValue != "" {
				out.Disks = append(out.Disks, infoDisk{Name: name, Text: textValue})
			}
		}
	}
	if signal != nil {
		if snr, ok := percent(firstSignalValue(signal, "snr", "SNR")); ok {
			sig := &infoSignal{SNR: snr, Standby: signalStandby(signal)}
			if db, ok := decibels(firstSignalValue(signal, "snr_db", "snrdb", "SNRdB")); ok {
				sig.SNRdB = strconv.FormatFloat(db, 'f', 1, 64)
			}
			sig.AGC, sig.HasAGC = percent(firstSignalValue(signal, "agc", "acg", "AGC"))
			sig.BER, sig.HasBER = signalCounter(firstSignalValue(signal, "ber", "BER"))
			if n := jsonText(firstSignalValue(signal, "tunernumber", "tuner_number")); n != "" {
				sig.Tuner = joinWords(jsonText(firstSignalValue(signal, "tunertype", "tuner_type")), "#"+n)
			}
			out.Signal = sig
		}
	}
	return out
}

var temperaturePaths = []string{"/proc/stb/sensors/temp0/value", "/proc/stb/fp/temp_sensor", "/proc/stb/fp/temp_sensor_avs"}

func parseTemperature(data []byte) (int, bool) {
	f, err := strconv.ParseFloat(strings.TrimSpace(string(data)), 64)
	if err != nil || f <= 0 || f > 150000 {
		return 0, false
	}
	if f > 150 {
		f /= 1000
	}
	if f < 1 || f > 150 {
		return 0, false
	}
	return int(f + 0.5), true
}

func (s *Server) deviceTemperature(ctx context.Context, profileID string) *int {
	sessionID := ""
	for _, info := range s.app.Files.Sessions() {
		if info.ProfileID == profileID {
			sessionID = info.ID
			break
		}
	}
	if sessionID == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	for _, p := range temperaturePaths {
		data, found, err := s.app.Files.ReadBytes(ctx, sessionID, p)
		if err != nil || !found {
			continue
		}
		if value, ok := parseTemperature(data); ok {
			return &value
		}
	}
	return nil
}

func getJSONMap(ctx context.Context, web deviceWeb, path string) (int, map[string]any, error) {
	status, body, err := web.getBounded(ctx, path, 1<<20)
	if err != nil || status != http.StatusOK {
		return status, nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return status, nil, fmt.Errorf("the receiver did not return JSON")
	}
	return status, m, nil
}

func (s *Server) deviceInfoHandler(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 12*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
	defer cancel()
	var (
		wg            sync.WaitGroup
		about, signal map[string]any
		aboutStatus   int
		aboutErr      error
		temperature   *int
		signalStatus  int
		signalErr     error
	)
	wg.Add(3)
	go func() { defer wg.Done(); aboutStatus, about, aboutErr = getJSONMap(ctx, web, "/api/about") }()
	go func() { defer wg.Done(); signalStatus, signal, signalErr = getJSONMap(ctx, web, "/api/tunersignal") }()
	go func() { defer wg.Done(); temperature = s.deviceTemperature(ctx, profile.ID) }()
	wg.Wait()
	if aboutErr != nil || aboutStatus != http.StatusOK {
		if aboutErr != nil && aboutStatus == http.StatusOK {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return its information.", "code": "web_error"})
			return
		}
		writeWebStatus(w, web, aboutStatus, aboutErr)
		return
	}
	if signalErr != nil || signalStatus != http.StatusOK {
		signal = nil
	}
	info := buildDeviceInfo(about, signal)
	info.Temperature = temperature
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, info)
}
