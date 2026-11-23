package httpapi

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"sort"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/config"
	"github.com/didou07/dcc-lite/src/filetransfer"
)

// recordings.epl is the recordings list of Enigma1 receivers (Dreambox DM500/DM5620/DM7000...).
// Its layout follows what the original DCC wrote:
//
//	#NAME RecMovies
//	#SERVICE: 1:0:1:<sid>:<tsid>:<onid>:c00000:0:0:0:/hdd/movie/<file>.ts
//	#DESCRIPTION: <title>
//	#END_DESCRIPTION
//	#TYPE 16385
//
// A recording copied to the disk by hand has no entry, so the receiver does not list it.
const (
	defaultEPLPath   = "/var/tuxbox/config/enigma/recordings.epl"
	defaultMovieDir  = "/hdd/movie"
	eplHeaderName    = "RecMovies"
	eplEntryType     = "16385"
	eplMaxListed     = 500
	eplBackupName    = "recordings.bak"
	eplReloadTimeout = 10 * time.Second
)

type eplEntry struct {
	start, end int    // line range [start, end) of the whole block
	file       string // base name of the recording the entry points to
}

type eplDocument struct {
	lines   []string
	entries []eplEntry
}

func parseEPL(text string) eplDocument {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if text == "" {
		lines = nil
	}
	doc := eplDocument{lines: lines}
	for i, line := range lines {
		if !strings.HasPrefix(line, "#SERVICE:") {
			continue
		}
		if n := len(doc.entries); n > 0 {
			doc.entries[n-1].end = i
		}
		doc.entries = append(doc.entries, eplEntry{start: i, end: len(lines), file: eplServiceFile(line)})
	}
	return doc
}

// eplServiceFile returns the recording's file name from a #SERVICE line, or "" for a service that is
// not a file (a live channel).
func eplServiceFile(line string) string {
	ref := strings.TrimSpace(strings.TrimPrefix(line, "#SERVICE:"))
	i := strings.Index(ref, ":/")
	if i < 0 {
		return ""
	}
	return path.Base(ref[i+1:])
}

type eplReport struct {
	Found    bool     `json:"found"`
	Path     string   `json:"path"`
	MovieDir string   `json:"movie_dir"`
	Entries  int      `json:"entries"`
	Movies   int      `json:"movies"`
	Lost     []string `json:"lost"`     // recordings on the disk that the list does not mention
	Dangling []string `json:"dangling"` // list entries whose file is not on the disk
	More     int      `json:"more,omitempty"`
}

// compareEPL works out which recordings are missing from the list and which list entries point at
// nothing. Only .ts files count as recordings.
func compareEPL(doc eplDocument, movies []string) (lost, dangling []string) {
	onDisk := map[string]bool{}
	for _, m := range movies {
		onDisk[m] = true
	}
	listed := map[string]bool{}
	for _, e := range doc.entries {
		if e.file == "" {
			continue
		}
		listed[e.file] = true
		if strings.HasSuffix(strings.ToLower(e.file), ".ts") && !onDisk[e.file] {
			dangling = append(dangling, e.file)
		}
	}
	for _, m := range movies {
		if !listed[m] {
			lost = append(lost, m)
		}
	}
	sort.Strings(lost)
	sort.Strings(dangling)
	return lost, dangling
}

func eplEntryText(movieDir, name string) string {
	title := strings.TrimSuffix(name, path.Ext(name))
	return "#SERVICE: 1:0:1:0:0:0:c00000:0:0:0:" + path.Join(movieDir, name) + "\n" +
		"#DESCRIPTION: " + title + "\n" +
		"#END_DESCRIPTION\n" +
		"#TYPE " + eplEntryType + "\n"
}

// rebuildEPL appends an entry for every lost recording and, when prune is set, drops the entries
// that point at nothing. Everything else stays exactly as it was.
func rebuildEPL(doc eplDocument, movieDir string, lost, dangling []string, prune bool) string {
	drop := map[string]bool{}
	if prune {
		for _, d := range dangling {
			drop[d] = true
		}
	}
	skip := make([]bool, len(doc.lines))
	for _, e := range doc.entries {
		if drop[e.file] {
			for i := e.start; i < e.end; i++ {
				skip[i] = true
			}
		}
	}
	var out strings.Builder
	wrote := false
	for i, line := range doc.lines {
		if skip[i] {
			continue
		}
		out.WriteString(line)
		out.WriteByte('\n')
		wrote = true
	}
	if !wrote {
		out.WriteString("#NAME " + eplHeaderName + "\n")
	}
	for _, name := range lost {
		out.WriteString(eplEntryText(movieDir, name))
	}
	return out.String()
}

func eplPaths(r *http.Request) (eplPath, movieDir string, err error) {
	eplPath = strings.TrimSpace(r.URL.Query().Get("epl"))
	if eplPath == "" {
		eplPath = defaultEPLPath
	}
	if eplPath, err = absRemotePath(eplPath); err != nil {
		return "", "", err
	}
	movieDir = strings.TrimSpace(r.URL.Query().Get("dir"))
	if movieDir == "" {
		movieDir = defaultMovieDir
	}
	if movieDir, err = absRemotePath(movieDir); err != nil {
		return "", "", err
	}
	return eplPath, movieDir, nil
}

func absRemotePath(p string) (string, error) {
	if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\r\n\x00") || len(p) > 300 {
		return "", errors.New("the path must be absolute, such as " + defaultEPLPath)
	}
	return path.Clean(p), nil
}

// listMovies returns the .ts recordings in the folder, skipping names that cannot be written into the
// list safely.
func (s *Server) listMovies(ctx context.Context, id, dir string) ([]string, error) {
	entries, err := s.app.Files.List(ctx, id, dir)
	if err != nil {
		return nil, err
	}
	var movies []string
	for _, e := range entries {
		if e.IsDir || !strings.HasSuffix(strings.ToLower(e.Name), ".ts") || strings.ContainsAny(e.Name, "\r\n\x00") {
			continue
		}
		movies = append(movies, e.Name)
	}
	return movies, nil
}

func (s *Server) readEPL(ctx context.Context, id, eplPath string) (text string, file filetransfer.TextFile, found bool, err error) {
	file, err = s.app.Files.ReadText(ctx, id, eplPath)
	if errors.Is(err, filetransfer.ErrEditNotFound) {
		return "", filetransfer.TextFile{}, false, nil
	}
	if err != nil {
		return "", filetransfer.TextFile{}, false, err
	}
	return file.Content, file, true, nil
}

func (s *Server) checkRecordingsEPL(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := s.sessionProfile(id); !ok {
		writeSessionNotFound(w)
		return
	}
	eplPath, movieDir, err := eplPaths(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	movies, err := s.listMovies(ctx, id, movieDir)
	if err != nil {
		writeEditError(w, err)
		return
	}
	text, _, found, err := s.readEPL(ctx, id, eplPath)
	if err != nil {
		writeEditError(w, err)
		return
	}
	doc := parseEPL(text)
	lost, dangling := compareEPL(doc, movies)
	report := eplReport{Found: found, Path: eplPath, MovieDir: movieDir, Entries: len(doc.entries), Movies: len(movies)}
	report.Lost, report.More = capNames(lost)
	report.Dangling, _ = capNames(dangling)
	writeJSON(w, http.StatusOK, report)
}

func capNames(names []string) ([]string, int) {
	if names == nil {
		names = []string{}
	}
	if len(names) > eplMaxListed {
		return names[:eplMaxListed], len(names) - eplMaxListed
	}
	return names, 0
}

func (s *Server) rebuildRecordingsEPL(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "recordings.epl rebuild requested")
	id := r.PathValue("id")
	profile, ok := s.sessionProfile(id)
	if !ok {
		writeSessionNotFound(w)
		return
	}
	eplPath, movieDir, err := eplPaths(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	prune := r.URL.Query().Get("prune") == "1"
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()

	movies, err := s.listMovies(ctx, id, movieDir)
	if err != nil {
		writeEditError(w, err)
		return
	}
	text, file, found, err := s.readEPL(ctx, id, eplPath)
	if err != nil {
		writeEditError(w, err)
		return
	}
	doc := parseEPL(text)
	lost, dangling := compareEPL(doc, movies)
	removed := 0
	if prune {
		removed = len(dangling)
	}
	if len(lost) == 0 && removed == 0 {
		writeJSON(w, http.StatusOK, map[string]any{"changed": false, "added": 0, "removed": 0, "message": "recordings.epl already lists every recording."})
		return
	}
	next := rebuildEPL(doc, movieDir, lost, dangling, prune)

	write := filetransfer.TextWrite{Content: next, LineEnding: file.LineEnding, Encoding: file.Encoding}
	if found {
		// The old list is kept first, as the original did, then replaced only if nobody changed it meanwhile.
		backup := path.Join(path.Dir(eplPath), eplBackupName)
		old := filetransfer.TextWrite{Content: text, LineEnding: file.LineEnding, Encoding: file.Encoding, Create: true}
		if _, err := s.app.Files.WriteText(ctx, id, backup, old); errors.Is(err, filetransfer.ErrEditExists) {
			old.Create, old.Force = false, true
			_, err = s.app.Files.WriteText(ctx, id, backup, old)
			if err != nil {
				writeEditError(w, fmt.Errorf("could not keep a copy of the old list, so nothing was changed: %w", err))
				return
			}
		} else if err != nil {
			writeEditError(w, fmt.Errorf("could not keep a copy of the old list, so nothing was changed: %w", err))
			return
		}
		write.ExpectedHash = file.Hash
		write.ExpectedSize = file.Size
		write.ExpectedModifiedAt = file.ModifiedAt
	} else {
		write.Create = true
		write.LineEnding = filetransfer.LineEndingLF
	}
	if _, err := s.app.Files.WriteText(ctx, id, eplPath, write); err != nil {
		writeEditError(w, err)
		return
	}
	reloaded, note := reloadRecordings(ctx, profile)
	writeJSON(w, http.StatusOK, map[string]any{
		"changed": true, "added": len(lost), "removed": removed,
		"backup": found, "reloaded": reloaded, "reload_note": note,
	})
}

// reloadRecordings asks an Enigma1 receiver to read recordings.epl again.
func reloadRecordings(ctx context.Context, profile config.Profile) (bool, string) {
	web := newDeviceWeb(profile, eplReloadTimeout)
	rctx, cancel := context.WithTimeout(ctx, eplReloadTimeout)
	defer cancel()
	resp, err := web.get(rctx, "/cgi-bin/reloadRecordings")
	if err != nil {
		return false, web.unreachable(err) + " Restart the receiver to see the changes."
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Sprintf("The receiver answered HTTP %d. Restart it to see the changes.", resp.StatusCode)
	}
	return true, ""
}
