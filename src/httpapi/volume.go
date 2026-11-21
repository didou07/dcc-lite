package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func legacyVolume(ctx context.Context, d deviceWeb, q url.Values) ([]byte, error) {
	keep := url.Values{}
	if v := q.Get("set"); v != "" {
		keep.Set("set", v)
	}
	var st struct {
		Result  string `xml:"e2result"`
		Current string `xml:"e2current"`
		Muted   string `xml:"e2ismuted"`
	}
	if err := d.legacyXML(ctx, withQuery("/web/vol", keep), 64<<10, &st); err != nil {
		return nil, err
	}
	return asJSON(map[string]any{"result": true, "current": num(st.Current), "ismute": xmlBool(st.Muted)})
}

type volumeRequest struct {
	Level *int `json:"level"`
	Mute  bool `json:"mute"`
}

func (s *Server) deviceVolume(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 8*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, keyPriorityKey{}, true)
	path := "/api/vol"
	if r.Method == http.MethodPost {
		var input volumeRequest
		if err := decodeJSON(w, r, &input); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		switch {
		case input.Mute:
			path += "?set=mute"
		case input.Level != nil && *input.Level >= 0 && *input.Level <= 100:
			path += "?set=set" + strconv.Itoa(*input.Level)
		default:
			writeError(w, http.StatusUnprocessableEntity, "the volume must be between 0 and 100")
			return
		}
	}
	status, body, err := web.pressRemote(ctx, path)
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	var reply struct {
		Current *float64 `json:"current"`
		IsMute  *bool    `json:"ismute"`
	}
	out := map[string]any{"ok": true}
	if json.Unmarshal([]byte(strings.TrimSpace(string(body))), &reply) == nil {
		if reply.Current != nil {
			out["level"] = int(*reply.Current)
		}
		if reply.IsMute != nil {
			out["muted"] = *reply.IsMute
		}
	}
	writeJSON(w, http.StatusOK, out)
}
