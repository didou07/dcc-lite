package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Package management goes through OpenWebif's /ipkg endpoint, which runs opkg on the receiver.
var packageNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9+._-]{0,99}$`)

// protectedPackages cannot be removed: doing so breaks the receiver or its web interface.
func protectedPackage(name string) bool {
	switch name {
	case "enigma2", "opkg", "busybox", "base-files", "base-passwd", "libc6", "udev", "systemd", "sysvinit", "initscripts", "enigma2-plugin-extensions-openwebif":
		return true
	}
	return strings.HasPrefix(name, "kernel") || strings.HasPrefix(name, "busybox-")
}

type packageEntry struct {
	Name        string `json:"name"`
	Version     string `json:"version"`
	Description string `json:"description,omitempty"`
	Section     string `json:"section,omitempty"`
	Installed   bool   `json:"installed"`
	UpgradeFrom string `json:"upgrade_from,omitempty"`
}

type packageCache struct {
	at   time.Time
	list []packageEntry
}

var (
	packageCaches   = map[string]packageCache{}
	packageCachesMu sync.Mutex
)

const (
	packageCacheTTL  = 2 * time.Minute
	maxPackageList   = 16 << 20
	maxPackageResult = 200
)

func parsePackageList(body []byte) ([]packageEntry, error) {
	var raw []struct {
		Name string `json:"name"`
		V    string `json:"v"`
		D    string `json:"d"`
		I    string `json:"i"`
		U    string `json:"u"`
		S    string `json:"s"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, err
	}
	out := make([]packageEntry, 0, len(raw))
	for _, p := range raw {
		if !packageNamePattern.MatchString(p.Name) {
			continue
		}
		e := packageEntry{Name: p.Name, Version: cleanText(p.V, 60), Description: cleanText(p.D, 240), Section: cleanText(p.S, 40), Installed: p.I == "1"}
		if p.U != "" && p.U != "0" {
			e.UpgradeFrom = cleanText(p.U, 60)
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (s *Server) devicePackages(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	query := r.URL.Query()
	fresh := query.Get("refresh") == "1"
	packageCachesMu.Lock()
	cached, have := packageCaches[profile.ID]
	packageCachesMu.Unlock()
	list := cached.list
	if !have || fresh || time.Since(cached.at) > packageCacheTTL {
		web := newDeviceWeb(profile, 150*time.Second)
		ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
		defer cancel()
		status, body, err := web.getBounded(ctx, "/ipkg?command=listall&format=json&filter=all", maxPackageList)
		if err == nil && status == http.StatusNotFound {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "This receiver's web interface has no package manager.", "code": "no_packages"})
			return
		}
		if writeWebStatus(w, web, status, err) {
			return
		}
		parsed, perr := parsePackageList(body)
		if perr != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": "The receiver did not return its package list.", "code": "web_error"})
			return
		}
		list = parsed
		packageCachesMu.Lock()
		packageCaches[profile.ID] = packageCache{at: time.Now(), list: list}
		packageCachesMu.Unlock()
	}
	needle := strings.ToLower(strings.TrimSpace(query.Get("q")))
	mode := query.Get("view")
	var installed, upgradable int
	out := make([]packageEntry, 0, maxPackageResult)
	matches := 0
	for _, p := range list {
		if p.Installed {
			installed++
		}
		if p.Installed && p.UpgradeFrom != "" {
			upgradable++
		}
		switch mode {
		case "installed":
			if !p.Installed {
				continue
			}
		case "updates":
			if !(p.Installed && p.UpgradeFrom != "") {
				continue
			}
		}
		if needle != "" && !strings.Contains(p.Name, needle) && !strings.Contains(strings.ToLower(p.Description), needle) {
			continue
		}
		matches++
		if len(out) < maxPackageResult {
			out = append(out, p)
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"total": len(list), "installed": installed, "upgradable": upgradable, "matches": matches, "packages": out})
}

type packageActionRequest struct {
	Command string `json:"command"`
	Package string `json:"package"`
}

func (s *Server) devicePackageAction(w http.ResponseWriter, r *http.Request) {
	var in packageActionRequest
	if err := decodeJSON(w, r, &in); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	query := "format=json&command="
	switch in.Command {
	case "update", "upgrade":
		query += in.Command
	case "install", "remove":
		if !packageNamePattern.MatchString(in.Package) {
			writeError(w, http.StatusBadRequest, "that is not a valid package name")
			return
		}
		if in.Command == "remove" && protectedPackage(in.Package) {
			writeError(w, http.StatusForbidden, in.Package+" is needed by the receiver and cannot be removed from here.")
			return
		}
		query += in.Command + "&package=" + url.QueryEscape(in.Package)
	default:
		writeError(w, http.StatusBadRequest, "unknown package action")
		return
	}
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	// The server gives up on a reply after three minutes, so stop a little earlier and say so.
	const limit = 165 * time.Second
	web := newDeviceWeb(profile, limit)
	ctx, cancel := context.WithTimeout(r.Context(), limit)
	defer cancel()
	status, body, err := web.getBounded(ctx, "/ipkg?"+query, 1<<20)
	packageCachesMu.Lock()
	delete(packageCaches, profile.ID)
	packageCachesMu.Unlock()
	if err != nil && ctx.Err() != nil {
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{"error": "The receiver is still working on it.", "code": "still_running"})
		return
	}
	if writeWebStatus(w, web, status, err) {
		return
	}
	var wrapped []struct {
		Result   bool     `json:"result"`
		Packages []string `json:"packages"`
	}
	lines := []string{}
	if json.Unmarshal(body, &wrapped) == nil && len(wrapped) > 0 {
		lines = wrapped[0].Packages
	} else {
		lines = strings.Split(string(body), "\n")
	}
	clean := make([]string, 0, len(lines))
	failed := false
	for _, line := range lines {
		line = cleanText(line, 300)
		if line == "" {
			continue
		}
		low := strings.ToLower(line)
		if strings.Contains(low, "collected errors") || strings.Contains(low, "unknown package") || strings.Contains(low, "cannot find package") || strings.Contains(low, "wget returned") {
			failed = true
		}
		clean = append(clean, line)
	}
	if len(clean) > 200 {
		clean = clean[len(clean)-200:]
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": !failed, "lines": clean})
}

type audioTrack struct {
	Index       int    `json:"index"`
	Description string `json:"description"`
	PID         int64  `json:"pid,omitempty"`
	Active      bool   `json:"active"`
}

func (s *Server) deviceAudio(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 10*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, m, err := getJSONMap(ctx, web, "/api/getaudiotracks")
	if writeWebStatus(w, web, status, err) {
		return
	}
	tracks := []audioTrack{}
	list, _ := m["tracklist"].([]any)
	for _, item := range list {
		t := asMap(item)
		idx := int(rawNum(json.RawMessage(jsonText(t["index"]))))
		tracks = append(tracks, audioTrack{Index: idx, Description: cleanText(jsonText(t["description"]), 120), PID: rawNum(json.RawMessage(jsonText(t["pid"]))), Active: rawBool(json.RawMessage(jsonText(t["active"])))})
		if len(tracks) >= 32 {
			break
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"tracks": tracks})
}

type audioSelectRequest struct {
	Index int `json:"index"`
}

func (s *Server) deviceAudioSelect(w http.ResponseWriter, r *http.Request) {
	var in audioSelectRequest
	if err := decodeJSON(w, r, &in); err != nil || in.Index < 0 || in.Index > 31 {
		writeError(w, http.StatusBadRequest, "choose one of the audio tracks")
		return
	}
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 10*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	status, body, err := web.press(ctx, "/api/selectaudiotrack?id="+strconv.Itoa(in.Index))
	if writeWebFailure(w, web, status, body, err) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
