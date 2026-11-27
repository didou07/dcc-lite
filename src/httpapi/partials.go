package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Helpers to find and delete stale ".<name>.dcclite-<id>.part" uploads.

const partialMinAge = 10 // minutes

var partialPathRe = regexp.MustCompile(`^/(?:[^/\x00-\x1f]+/)*\.[^/\x00-\x1f]+\.dcclite-[A-Za-z0-9-]+\.part$`)

func partialPathOK(p string) bool {
	if len(p) > 600 || !partialPathRe.MatchString(p) {
		return false
	}
	for _, prefix := range []string{"/proc/", "/sys/", "/dev/"} {
		if strings.HasPrefix(p, prefix) {
			return false
		}
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == ".." || seg == "." || len(seg) > 255 {
			return false
		}
	}
	return true
}

const partialScanCommand = `if ! find / -maxdepth 0 -mmin +1 >/dev/null 2>&1; then echo NOMMIN; else ` +
	`M=$(awk '$2 ~ /^\/(media|mnt)\// && $3 !~ /^(nfs4?|cifs|smb3|smbfs)$/ {print $2}' /proc/mounts); ` +
	`for D in / $M; do case "$D" in *\\*) continue;; esac; find "$D" -xdev -type f -name '.*.dcclite-*.part' -mmin +10 2>/dev/null; done | head -n 200 | ` +
	`while IFS= read -r F; do echo "f=$(wc -c < "$F" 2>/dev/null)|$F"; done; echo SCANDONE; fi`

type partialFile struct {
	Path string `json:"path"`
	Size int64  `json:"size"`
}

func parsePartialScan(out string) (files []partialFile, noMmin, done bool) {
	files = []partialFile{}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
		line = strings.TrimRight(line, " \t")
		switch {
		case line == "NOMMIN":
			noMmin = true
		case line == "SCANDONE":
			done = true
		case strings.HasPrefix(line, "f="):
			size, path, ok := strings.Cut(strings.TrimPrefix(line, "f="), "|")
			if !ok || !partialPathOK(path) || seen[path] {
				continue
			}
			seen[path] = true
			n, _ := strconv.ParseInt(strings.TrimSpace(size), 10, 64)
			files = append(files, partialFile{Path: path, Size: n})
		}
	}
	return files, noMmin, done
}

func partialDeleteCommand(paths []string) string {
	var b strings.Builder
	for _, p := range paths {
		q := shellQuote(p)
		b.WriteString(`if [ -n "$(find ` + q + ` -maxdepth 0 -type f -mmin +10 2>/dev/null)" ] && rm -f ` + q + `; then echo DCCOK; else echo DCCFAIL; fi; `)
	}
	return strings.TrimSuffix(b.String(), "; ")
}

// partialChunks splits the paths so that no command gets longer than the Telnet runner accepts.
func partialChunks(paths []string) [][]string {
	var chunks [][]string
	var cur []string
	size := 0
	for _, p := range paths {
		n := 2*len(shellQuote(p)) + 150
		if len(cur) > 0 && size+n > 4500 {
			chunks = append(chunks, cur)
			cur, size = nil, 0
		}
		cur = append(cur, p)
		size += n
	}
	if len(cur) > 0 {
		chunks = append(chunks, cur)
	}
	return chunks
}

func (s *Server) partialsScan(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	out, _, err := (shellRunner{profile}).run(ctx, 80*time.Second, partialScanCommand, 256<<10)
	if err != nil {
		telnetFailure(w, profile, err)
		return
	}
	files, noMmin, done := parsePartialScan(out)
	switch {
	case noMmin:
		writeError(w, http.StatusNotImplemented, "This receiver's find command cannot tell old files from running uploads, so nothing was scanned.")
		return
	case !done:
		writeError(w, http.StatusBadGateway, "The search on the receiver did not finish.")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"files": files, "min_age": partialMinAge})
}

type partialDeleteRequest struct {
	Paths []string `json:"paths"`
}

func (s *Server) partialsDelete(w http.ResponseWriter, r *http.Request) {
	logAction(w, r, "partials delete")
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var in partialDeleteRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&in); err != nil || len(in.Paths) == 0 || len(in.Paths) > 100 {
		writeError(w, http.StatusBadRequest, "Invalid request.")
		return
	}
	for _, p := range in.Paths {
		if !partialPathOK(p) {
			writeError(w, http.StatusBadRequest, "Only unfinished uploads made by DCC Lite can be deleted.")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 150*time.Second)
	defer cancel()
	run := shellRunner{profile}
	deleted, failed := 0, 0
	for _, chunk := range partialChunks(in.Paths) {
		out, _, err := run.run(ctx, 40*time.Second, partialDeleteCommand(chunk), 8<<10)
		if err != nil {
			telnetFailure(w, profile, err)
			return
		}
		got := 0
		for _, line := range strings.Split(strings.ReplaceAll(out, "\r", ""), "\n") {
			switch strings.TrimSpace(line) {
			case "DCCOK":
				deleted++
				got++
			case "DCCFAIL":
				failed++
				got++
			}
		}
		failed += len(chunk) - got
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": deleted, "failed": failed})
}
