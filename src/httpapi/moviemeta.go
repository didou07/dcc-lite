package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

var movieMediaFileRe = regexp.MustCompile(`(?i)\.(ts|mkv|mp4|avi|mpg|mpeg|m2ts|mts|mov|vob|iso|flv|wmv|m4v|mp3|flac|ogg|m4a|wav)$`)

const (
	movieMaxPath   = 600
	movieMaxTitle  = 150
	movieMaxDesc   = 300
	movieMaxTags   = 20
	movieMaxTagLen = 40
	movieMetaLines = 24
)

type movieMeta struct {
	Exists      bool   `json:"exists"`
	Ref         string `json:"ref,omitempty"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Time        int64  `json:"time,omitempty"`
	Tags        string `json:"tags"`
}

func movieFileOK(file string) bool {
	return moviePathOK(file) && len(file) <= movieMaxPath && movieMediaFileRe.MatchString(file) &&
		!strings.ContainsFunc(file, unicode.IsControl) && !strings.HasSuffix(file, "/")
}

func movieMetaReadCommand(file string) string {
	f := shellQuote(file + ".meta")
	return `if [ -f ` + f + ` ]; then echo @@meta; head -c 16384 ` + f + `; else echo @@nometa; fi`
}

func splitMetaLines(out string) (lines []string, exists bool) {
	out = strings.ReplaceAll(out, "\r", "")
	rest, ok := strings.CutPrefix(strings.TrimLeft(out, "\n"), "@@meta\n")
	if !ok {
		return nil, false
	}
	rest = strings.TrimSuffix(rest, "\n")
	if rest == "" {
		return []string{}, true
	}
	return strings.Split(rest, "\n"), true
}

func parseMovieMeta(lines []string, exists bool) movieMeta {
	meta := movieMeta{Exists: exists}
	get := func(i int) string {
		if i < len(lines) {
			return strings.TrimSpace(lines[i])
		}
		return ""
	}
	meta.Ref, meta.Title, meta.Description, meta.Tags = get(0), get(1), get(2), get(4)
	meta.Time, _ = strconv.ParseInt(get(3), 10, 64)
	return meta
}

func cleanMovieTags(text string) string {
	seen := map[string]bool{}
	var out []string
	for _, tag := range strings.Fields(text) {
		tag = cleanText(tag, movieMaxTagLen)
		if tag == "" || seen[strings.ToLower(tag)] {
			continue
		}
		seen[strings.ToLower(tag)] = true
		out = append(out, tag)
		if len(out) >= movieMaxTags {
			break
		}
	}
	return strings.Join(out, " ")
}

type movieMetaRequest struct {
	File        string `json:"file"`
	Ref         string `json:"ref"`
	Time        int64  `json:"time"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Tags        string `json:"tags"`
}

// movieMetaWriteCommand rewrites the title, description and tags of a .meta file.
func movieMetaWriteCommand(file string, current []string, exists bool, in movieMetaRequest) (string, error) {
	title := cleanText(in.Title, movieMaxTitle)
	if title == "" {
		return "", badDisk("Type a title.")
	}
	lines := append([]string{}, current...)
	if !exists || len(lines) == 0 {
		if !movieRefOK(in.Ref) {
			return "", badDisk("This recording has no information file and cannot be described.")
		}
		lines = []string{in.Ref, "", "", strconv.FormatInt(max(in.Time, 0), 10), ""}
	}
	if len(lines) > movieMetaLines {
		return "", badDisk("The recording's information file is not in the expected format.")
	}
	for len(lines) < 5 {
		lines = append(lines, "")
	}
	lines[1], lines[2], lines[4] = title, cleanText(in.Description, movieMaxDesc), cleanMovieTags(in.Tags)
	args := make([]string, len(lines))
	for i, line := range lines {
		args[i] = shellQuote(strings.ReplaceAll(line, "\x00", ""))
	}
	f, t := shellQuote(file+".meta"), shellQuote(file+".meta.dcc")
	cmd := `printf '%s\n' ` + strings.Join(args, " ") + ` > ` + t + ` && mv ` + t + ` ` + f + ` && echo SAVED || { rm -f ` + t + `; echo FAILED; }`
	if len(cmd) > 5800 {
		return "", badDisk("The text is too long.")
	}
	return cmd, nil
}

func (s *Server) movieMetaGet(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	file := r.URL.Query().Get("file")
	if !movieFileOK(file) {
		writeError(w, http.StatusBadRequest, "not a recording")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	out, _, err := (shellRunner{profile}).run(ctx, 15*time.Second, movieMetaReadCommand(file), 64<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	lines, exists := splitMetaLines(out)
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, parseMovieMeta(lines, exists))
}

func (s *Server) movieMetaSave(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "movie meta save")
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var in movieMetaRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 16<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	if !movieFileOK(in.File) {
		writeError(w, http.StatusBadRequest, "not a recording")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	run := shellRunner{profile}
	out, _, err := run.run(ctx, 15*time.Second, movieMetaReadCommand(in.File), 64<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	lines, exists := splitMetaLines(out)
	cmd, err := movieMetaWriteCommand(in.File, lines, exists, in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, _, err = run.run(ctx, 15*time.Second, cmd, 4<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	saved := strings.Contains(out, "SAVED")
	writeJSON(w, http.StatusOK, map[string]any{"ok": saved, "status": map[bool]string{true: "saved", false: "failed"}[saved]})
}

type movieFolderRequest struct {
	Action   string `json:"action"`
	Dir      string `json:"dir"`
	Name     string `json:"name"`
	Bookmark bool   `json:"bookmark"`
}

var movieFolderNameRe = regexp.MustCompile(`^[^/\x00-\x1f\\]{1,100}$`)

func movieFolderPath(dir string) (string, error) {
	dir = strings.TrimRight(strings.TrimSpace(dir), "/")
	if dir == "" || !moviePathOK(dir) || len(dir) > movieMaxPath || strings.ContainsFunc(dir, unicode.IsControl) || strings.Count(dir, "/") < 3 {
		return "", badDisk("Choose a folder inside a drive, for example /media/hdd/movie/Series.")
	}
	return dir, nil
}

func movieFolderCommand(req movieFolderRequest) (string, error) {
	dir, err := movieFolderPath(req.Dir)
	if err != nil {
		return "", err
	}
	q := shellQuote(dir)
	switch req.Action {
	case "create":
		return `if [ -e ` + q + ` ]; then echo EXISTS; elif mkdir -p ` + q + `; then echo CREATED; else echo FAILED; fi`, nil
	case "remove":
		return `if [ ! -d ` + q + ` ]; then echo MISSING; elif grep -F -q ` + shellQuote(" "+fstabEscape(dir)+" ") + ` /proc/mounts; then echo ISMOUNT; elif rmdir ` + q + ` 2>/dev/null; then echo REMOVED; else echo NOTEMPTY; fi`, nil
	case "rename":
		if !movieFolderNameRe.MatchString(req.Name) || req.Name == "." || req.Name == ".." {
			return "", badDisk("The folder name must not be empty or contain a slash.")
		}
		target := path.Join(path.Dir(dir), req.Name)
		if _, err := movieFolderPath(target); err != nil {
			return "", err
		}
		n := shellQuote(target)
		return `if [ ! -d ` + q + ` ]; then echo MISSING; elif grep -F -q ` + shellQuote(" "+fstabEscape(dir)+" ") + ` /proc/mounts; then echo ISMOUNT; elif [ -e ` + n + ` ]; then echo EXISTS; elif mv ` + q + ` ` + n + `; then echo RENAMED; else echo FAILED; fi`, nil
	}
	return "", badDisk("Invalid request.")
}

func movieFolderStatus(out string) string {
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		switch w := strings.TrimSpace(line); w {
		case "CREATED", "EXISTS", "REMOVED", "NOTEMPTY", "ISMOUNT", "MISSING", "RENAMED", "FAILED":
			return strings.ToLower(w)
		}
	}
	return ""
}

func (s *Server) movieFolder(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "movie folder")
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var in movieFolderRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<10)).Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()

	switch in.Action {
	case "bookmark-add", "bookmark-remove":
		dir, err := movieFolderPath(in.Dir)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		call := "/api/addlocation?dirname="
		if in.Action == "bookmark-remove" {
			call = "/api/removelocation?dirname="
		}
		web := newDeviceWeb(profile, 15*time.Second)
		status, body, err := web.press(ctx, call+url.QueryEscape(dir+"/"))
		if writeWebFailure(w, web, status, body, err) {
			return
		}
		var reply struct {
			Result  *bool  `json:"result"`
			Message string `json:"message"`
		}
		_ = json.Unmarshal(body, &reply)
		good := reply.Result == nil || *reply.Result
		writeJSON(w, http.StatusOK, map[string]any{"ok": good, "status": strings.TrimPrefix(in.Action, "bookmark-"), "output": cleanText(reply.Message, 300)})
		return
	}

	cmd, err := movieFolderCommand(in)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	out, _, err := (shellRunner{profile}).run(ctx, 20*time.Second, cmd, 4<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	status := movieFolderStatus(out)
	good := status == "created" || status == "removed" || status == "renamed"
	if good && in.Action == "create" && in.Bookmark {
		dir, _ := movieFolderPath(in.Dir)
		web := newDeviceWeb(profile, 15*time.Second)
		_, _, _ = web.press(ctx, "/api/addlocation?dirname="+url.QueryEscape(dir+"/"))
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": good, "status": status})
}
