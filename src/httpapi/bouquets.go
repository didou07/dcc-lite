package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/enigma2"
	"github.com/didou07/dcc-lite/src/filetransfer"
)

const defaultEnigma2Dir = "/etc/enigma2"

type nameCache struct {
	mu      sync.Mutex
	entries map[string]nameCacheEntry
}

type nameCacheEntry struct {
	names map[enigma2.ServiceKey]string
	at    time.Time
}

const nameCacheTTL = 10 * time.Minute

func (c *nameCache) get(key string) (map[enigma2.ServiceKey]string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || time.Since(entry.at) > nameCacheTTL {
		delete(c.entries, key)
		return nil, false
	}
	return entry.names, true
}

func (c *nameCache) put(key string, names map[enigma2.ServiceKey]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]nameCacheEntry)
	}
	if len(c.entries) > 16 {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	c.entries[key] = nameCacheEntry{names: names, at: time.Now()}
}

func bouquetDir(raw string) (string, error) {
	dir := strings.TrimSpace(raw)
	if dir == "" {
		return defaultEnigma2Dir, nil
	}
	if !strings.HasPrefix(dir, "/") || strings.ContainsAny(dir, "\r\n\x00") || len(dir) > 300 {
		return "", errors.New("the settings folder must be an absolute path such as /etc/enigma2")
	}
	return strings.TrimRight(path.Clean(dir), "/") + "", nil
}

func writeBouquetError(w http.ResponseWriter, err error) {
	var perr bouquetInputError
	if errors.As(err, &perr) {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "code": "bad_request"})
		return
	}
	writeEditError(w, err)
}

type bouquetInputError struct{ msg string }

func (e bouquetInputError) Error() string { return e.msg }

func badInput(format string, args ...any) error {
	return bouquetInputError{fmt.Sprintf(format, args...)}
}

func (s *Server) readChannelNames(ctx context.Context, sessionID, dir string, refresh bool) (map[enigma2.ServiceKey]string, bool) {
	key := sessionID + "|" + dir
	if !refresh {
		if names, ok := s.names.get(key); ok {
			return names, len(names) > 0
		}
	}
	merged := map[enigma2.ServiceKey]string{}
	for _, file := range []string{"lamedb", "lamedb5"} {
		data, found, err := s.app.Files.ReadBytes(ctx, sessionID, dir+"/"+file)
		if err != nil || !found {
			continue
		}
		for k, v := range enigma2.ParseLamedb(strings.ToValidUTF8(string(data), "")) {
			merged[k] = v
		}
	}
	s.names.put(key, merged)
	return merged, len(merged) > 0
}

type bouquetSummary struct {
	File string `json:"file"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

func (s *Server) listBouquets(w http.ResponseWriter, r *http.Request) {
	dir, err := bouquetDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	id := r.PathValue("id")
	out := map[string]any{"dir": dir}
	foundAny := false
	for _, kind := range []string{"tv", "radio"} {
		list := []bouquetSummary{}
		data, found, err := s.app.Files.ReadBytes(r.Context(), id, dir+"/bouquets."+kind)
		if err != nil {
			writeBouquetError(w, err)
			return
		}
		if found {
			foundAny = true
			idx, perr := enigma2.ParseIndex(string(data))
			if perr != nil {
				writeError(w, http.StatusUnprocessableEntity, "bouquets."+kind+": "+perr.Error())
				return
			}
			for _, file := range idx.Files() {
				name := file
				if file, ferr := s.app.Files.ReadText(r.Context(), id, dir+"/"+file); ferr == nil {
					if parsed, perr := enigma2.ParseBouquet(file.Content); perr == nil && parsed.Name != "" {
						name = parsed.Name
					}
				}
				list = append(list, bouquetSummary{File: file, Name: name, Kind: kind})
			}
		}
		out[kind] = list
	}
	if !foundAny {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no bouquets.tv or bouquets.radio found in " + dir, "code": "file_not_found"})
		return
	}
	writeJSON(w, http.StatusOK, out)
}

type bouquetEntryView struct {
	Ref         string `json:"ref"`
	Description string `json:"description"`
	Name        string `json:"name"`
	Base        string `json:"base,omitempty"`
	Marker      bool   `json:"marker"`
}

func (s *Server) getBouquet(w http.ResponseWriter, r *http.Request) {
	dir, err := bouquetDir(r.URL.Query().Get("dir"))
	file := r.URL.Query().Get("file")
	if err != nil || !enigma2.ValidBouquetFile(file) {
		writeError(w, http.StatusBadRequest, "a valid bouquet file name is required")
		return
	}
	id := r.PathValue("id")
	text, err := s.app.Files.ReadText(r.Context(), id, dir+"/"+file)
	if err != nil {
		writeEditError(w, err)
		return
	}
	bouquet, err := enigma2.ParseBouquet(text.Content)
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	names, haveNames := s.readChannelNames(r.Context(), id, dir, r.URL.Query().Get("refresh") == "1")
	entries := make([]bouquetEntryView, 0, len(bouquet.Entries))
	for _, e := range bouquet.Entries {
		base := ""
		if !e.IsMarker() {
			base = enigma2.DisplayName(enigma2.Entry{Ref: e.Ref}, names)
		}
		entries = append(entries, bouquetEntryView{Ref: e.Ref, Description: e.Description, Name: enigma2.DisplayName(e, names), Base: base, Marker: e.IsMarker()})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"file": file, "kind": enigma2.KindOf(file), "name": bouquet.Name, "entries": entries,
		"names_available": haveNames, "hash": text.Hash, "size": text.Size, "modified_at": text.ModifiedAt,
	})
}

func (s *Server) saveBouquet(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "bouquet save requested")
	var input struct {
		Dir     string `json:"dir"`
		File    string `json:"file"`
		Name    string `json:"name"`
		Entries []struct {
			Ref         string `json:"ref"`
			Description string `json:"description"`
		} `json:"entries"`
		ExpectedHash string `json:"expected_hash"`
		ExpectedSize int64  `json:"expected_size"`
		Force        bool   `json:"force"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dir, err := bouquetDir(input.Dir)
	if err != nil || !enigma2.ValidBouquetFile(input.File) {
		writeError(w, http.StatusBadRequest, "a valid bouquet file name is required")
		return
	}
	name := strings.TrimSpace(input.Name)
	if name == "" || len(name) > 120 || strings.ContainsAny(name, "\r\n\x00") {
		writeError(w, http.StatusBadRequest, "the bouquet name must be 1 to 120 characters on one line")
		return
	}
	if len(input.Entries) > enigma2.MaxEntries {
		writeError(w, http.StatusBadRequest, "the bouquet has too many entries")
		return
	}
	id := r.PathValue("id")
	remote := dir + "/" + input.File

	current, err := s.app.Files.ReadText(r.Context(), id, remote)
	if err != nil {
		writeEditError(w, err)
		return
	}
	if !input.Force && input.ExpectedHash != "" && current.Hash != input.ExpectedHash {
		writeEditError(w, filetransfer.ErrEditConflict)
		return
	}
	existing, perr := enigma2.ParseBouquet(current.Content)
	if perr != nil {
		writeError(w, http.StatusUnprocessableEntity, perr.Error())
		return
	}
	bouquet := enigma2.Bouquet{Name: name, Header: existing.Header}
	for _, e := range input.Entries {
		entry := enigma2.Entry{Ref: strings.TrimSpace(e.Ref), Description: e.Description, HasDescription: e.Description != ""}
		if err := enigma2.ValidateEntry(entry); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		bouquet.Entries = append(bouquet.Entries, entry)
	}
	saved, err := s.app.Files.WriteText(r.Context(), id, remote, filetransfer.TextWrite{
		Content: bouquet.Format(), LineEnding: filetransfer.LineEndingLF,
		ExpectedSize: current.Size, ExpectedModifiedAt: current.ModifiedAt, ExpectedHash: current.Hash, Force: input.Force,
	})
	if err != nil {
		writeEditError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hash": saved.Hash, "size": saved.Size, "modified_at": saved.ModifiedAt, "count": len(bouquet.Entries)})
}

func randomSuffix() (string, error) {
	raw := make([]byte, 3)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func (s *Server) newBouquet(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "new bouquet requested")
	var input struct {
		Dir  string `json:"dir"`
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dir, err := bouquetDir(input.Dir)
	name := strings.TrimSpace(input.Name)
	if err != nil || (input.Kind != "tv" && input.Kind != "radio") || name == "" || len(name) > 120 || strings.ContainsAny(name, "\r\n\x00") {
		writeError(w, http.StatusBadRequest, "a bouquet name (1-120 characters) and kind (tv or radio) are required")
		return
	}
	id := r.PathValue("id")
	indexPath := dir + "/bouquets." + input.Kind
	index, err := s.readIndex(r.Context(), id, indexPath)
	if err != nil {
		writeBouquetError(w, err)
		return
	}
	suffix, err := randomSuffix()
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	file := "userbouquet.dcc" + suffix + "." + input.Kind
	if _, err := s.app.Files.WriteText(r.Context(), id, dir+"/"+file, filetransfer.TextWrite{
		Content: enigma2.Bouquet{Name: name}.Format(), LineEnding: filetransfer.LineEndingLF, Create: true,
	}); err != nil {
		writeEditError(w, err)
		return
	}
	index.text.Add(file)
	if _, err := s.app.Files.WriteText(r.Context(), id, indexPath, filetransfer.TextWrite{
		Content: index.text.Format(), LineEnding: filetransfer.LineEndingLF,
		ExpectedSize: index.file.Size, ExpectedModifiedAt: index.file.ModifiedAt, ExpectedHash: index.file.Hash,
	}); err != nil {

		_ = s.app.Files.Remove(context.WithoutCancel(r.Context()), id, dir+"/"+file, false)
		writeEditError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"file": file, "name": name, "kind": input.Kind})
}

type loadedIndex struct {
	text enigma2.Index
	file filetransfer.TextFile
}

func (s *Server) readIndex(ctx context.Context, id, indexPath string) (loadedIndex, error) {
	file, err := s.app.Files.ReadText(ctx, id, indexPath)
	if err != nil {
		return loadedIndex{}, err
	}
	parsed, err := enigma2.ParseIndex(file.Content)
	if err != nil {
		return loadedIndex{}, badInput("%s: %s", path.Base(indexPath), err.Error())
	}
	return loadedIndex{text: parsed, file: file}, nil
}

func (s *Server) changeIndex(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Dir    string `json:"dir"`
		File   string `json:"file"`
		Action string `json:"action"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	dir, err := bouquetDir(input.Dir)
	if err != nil || !enigma2.ValidBouquetFile(input.File) || (input.Action != "remove" && input.Action != "delete" && input.Action != "up" && input.Action != "down") {
		writeError(w, http.StatusBadRequest, "a valid bouquet file and action (remove, delete, up, down) are required")
		return
	}
	id := r.PathValue("id")
	indexPath := dir + "/bouquets." + enigma2.KindOf(input.File)
	index, err := s.readIndex(r.Context(), id, indexPath)
	if err != nil {
		writeBouquetError(w, err)
		return
	}
	changed := false
	switch input.Action {
	case "remove", "delete":
		changed = index.text.Remove(input.File)
	case "up":
		changed = index.text.Move(input.File, -1)
	case "down":
		changed = index.text.Move(input.File, 1)
	}
	if !changed {
		writeJSON(w, http.StatusOK, map[string]any{"changed": false})
		return
	}
	if _, err := s.app.Files.WriteText(r.Context(), id, indexPath, filetransfer.TextWrite{
		Content: index.text.Format(), LineEnding: filetransfer.LineEndingLF,
		ExpectedSize: index.file.Size, ExpectedModifiedAt: index.file.ModifiedAt, ExpectedHash: index.file.Hash,
	}); err != nil {
		writeEditError(w, err)
		return
	}
	if input.Action == "delete" {

		if err := s.app.Files.Remove(r.Context(), id, dir+"/"+input.File, false); err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"changed": true, "file_removed": false, "note": "The bouquet was removed from the list, but its file could not be deleted: " + err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"changed": true, "file_removed": true})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changed": true})
}

func (s *Server) reloadBouquets(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var profileID string
	for _, info := range s.app.Files.Sessions() {
		if info.ID == id {
			profileID = info.ProfileID
		}
	}
	profile, ok := s.app.Profiles.Get(profileID)
	if !ok {
		writeError(w, http.StatusNotFound, "device profile not found")
		return
	}
	web := newDeviceWeb(profile, 8*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	resp, err := web.get(ctx, "/web/servicelistreload?mode=2")
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": web.unreachable(err) + " You can also reload from the Terminal (Saved commands → Reload bouquets) or restart Enigma2.", "code": "web_unreachable"})
		return
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": fmt.Sprintf("The web interface answered HTTP %d.", resp.StatusCode), "code": "web_error"})
		return
	}
	answer := strings.TrimSpace(string(body))
	if len(answer) > 200 {
		answer = answer[:200]
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "answer": answer})
}

var serviceIndex = &serviceCache{}

type serviceCache struct {
	mu      sync.Mutex
	entries map[string]serviceCacheEntry
}

type serviceCacheEntry struct {
	services []enigma2.Service
	at       time.Time
}

func (c *serviceCache) get(key string) ([]enigma2.Service, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.entries[key]
	if !ok || time.Since(entry.at) > nameCacheTTL {
		delete(c.entries, key)
		return nil, false
	}
	return entry.services, true
}

func (c *serviceCache) put(key string, services []enigma2.Service) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]serviceCacheEntry)
	}
	if len(c.entries) > 8 {
		for k := range c.entries {
			delete(c.entries, k)
			break
		}
	}
	c.entries[key] = serviceCacheEntry{services: services, at: time.Now()}
}

func (c *serviceCache) forget() {
	c.mu.Lock()
	c.entries = nil
	c.mu.Unlock()
}

const maxServiceResults = 1500

func (s *Server) listServices(w http.ResponseWriter, r *http.Request) {
	dir, err := bouquetDir(r.URL.Query().Get("dir"))
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	kind := r.URL.Query().Get("kind")
	if kind != "tv" && kind != "radio" {
		writeError(w, http.StatusBadRequest, "kind must be tv or radio")
		return
	}
	id := r.PathValue("id")
	key := id + "|" + dir
	all, ok := serviceIndex.get(key)
	if !ok || r.URL.Query().Get("refresh") == "1" {
		all = nil
		for _, file := range []string{"lamedb", "lamedb5"} {
			data, found, readErr := s.app.Files.ReadBytes(r.Context(), id, dir+"/"+file)
			if readErr != nil {
				writeBouquetError(w, readErr)
				return
			}
			if !found {
				continue
			}
			all = mergeServices(all, enigma2.ParseServices(strings.ToValidUTF8(string(data), "")))
		}
		serviceIndex.put(key, all)
	}
	if len(all) == 0 {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "no channels found: there is no lamedb in " + dir, "code": "file_not_found"})
		return
	}
	needle := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	provider := strings.TrimSpace(r.URL.Query().Get("provider"))
	matches := make([]enigma2.Service, 0, 64)
	providerCount := map[string]int{}
	total := 0
	for _, svc := range all {
		if kind == "radio" {
			if !svc.Radio {
				continue
			}
		} else if !enigma2.IsTVType(svc.Type) {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(svc.Name), needle) && !strings.Contains(strings.ToLower(svc.Provider), needle) {
			continue
		}
		if svc.Provider != "" {
			providerCount[svc.Provider]++
		}
		if provider != "" && svc.Provider != provider {
			continue
		}
		total++
		matches = append(matches, svc)
	}
	type providerInfo struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	providers := make([]providerInfo, 0, len(providerCount))
	for name, count := range providerCount {
		providers = append(providers, providerInfo{name, count})
	}
	sort.Slice(providers, func(i, j int) bool { return strings.ToLower(providers[i].Name) < strings.ToLower(providers[j].Name) })
	if len(providers) > 400 {
		providers = providers[:400]
	}
	sort.SliceStable(matches, func(i, j int) bool {
		return strings.ToLower(matches[i].Name) < strings.ToLower(matches[j].Name)
	})
	truncated := false
	if len(matches) > maxServiceResults {
		matches, truncated = matches[:maxServiceResults], true
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": matches, "total": total, "truncated": truncated, "providers": providers})
}

func mergeServices(base, next []enigma2.Service) []enigma2.Service {
	index := make(map[string]int, len(base))
	for i, svc := range base {
		index[svc.Ref] = i
	}
	for _, svc := range next {
		if i, ok := index[svc.Ref]; ok {
			base[i] = svc
		} else {
			index[svc.Ref] = len(base)
			base = append(base, svc)
		}
	}
	return base
}
