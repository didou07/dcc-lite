package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const eplSample = "#NAME RecMovies\n" +
	"#SERVICE: 1:0:1:1:2:3:c00000:0:0:0:/hdd/movie/old.ts\n#DESCRIPTION: Old\n#END_DESCRIPTION\n#TYPE 16385\n" +
	"#SERVICE: 1:0:1:1:2:3:c00000:0:0:0:/hdd/movie/gone.ts\n#DESCRIPTION: Gone\n#END_DESCRIPTION\n#TYPE 16385\n"

func TestParseAndCompareEPL(t *testing.T) {
	doc := parseEPL(eplSample)
	if len(doc.entries) != 2 || doc.entries[0].file != "old.ts" || doc.entries[1].file != "gone.ts" {
		t.Fatalf("entries: %+v", doc.entries)
	}
	if doc.entries[0].end != doc.entries[1].start {
		t.Fatalf("blocks should be contiguous: %+v", doc.entries)
	}
	lost, dangling := compareEPL(doc, []string{"old.ts", "new.ts", "also new.ts"})
	if strings.Join(lost, "|") != "also new.ts|new.ts" || strings.Join(dangling, "|") != "gone.ts" {
		t.Fatalf("lost %v dangling %v", lost, dangling)
	}
}

func TestRebuildEPLKeepsEverythingElse(t *testing.T) {
	doc := parseEPL(eplSample)
	lost, dangling := compareEPL(doc, []string{"old.ts", "new.ts"})
	kept := rebuildEPL(doc, "/hdd/movie", lost, dangling, false)
	if !strings.HasPrefix(kept, eplSample) || !strings.Contains(kept, "gone.ts") {
		t.Fatalf("existing entries must stay untouched:\n%s", kept)
	}
	want := "#SERVICE: 1:0:1:0:0:0:c00000:0:0:0:/hdd/movie/new.ts\n#DESCRIPTION: new\n#END_DESCRIPTION\n#TYPE 16385\n"
	if !strings.HasSuffix(kept, want) {
		t.Fatalf("new entry wrong:\n%s", kept)
	}
	pruned := rebuildEPL(doc, "/hdd/movie", lost, dangling, true)
	if strings.Contains(pruned, "gone.ts") || !strings.Contains(pruned, "old.ts") || !strings.Contains(pruned, "new.ts") {
		t.Fatalf("prune wrong:\n%s", pruned)
	}
	empty := rebuildEPL(parseEPL(""), "/hdd/movie", []string{"a.ts"}, nil, false)
	if !strings.HasPrefix(empty, "#NAME RecMovies\n#SERVICE:") {
		t.Fatalf("a new list needs its header:\n%s", empty)
	}
}

func (h *harness) eplCall(method, query string) (int, map[string]any) {
	h.t.Helper()
	url := h.srv.URL + h.url("/recordings/epl") + query
	if method == "POST" {
		url = h.srv.URL + h.url("/recordings/epl/rebuild") + query
	}
	req, _ := http.NewRequest(method, url, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		h.t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return resp.StatusCode, out
}

func TestRecordingsEPLCheckAndRebuild(t *testing.T) {
	h := newHarness(t)
	h.ftp.MkDir("/hdd/movie")
	h.ftp.MkDir("/var/tuxbox/config/enigma")
	h.ftp.Put("/var/tuxbox/config/enigma/recordings.epl", []byte(eplSample))
	h.ftp.Put("/hdd/movie/old.ts", []byte("x"))
	h.ftp.Put("/hdd/movie/lost one.ts", []byte("x"))
	h.ftp.Put("/hdd/movie/notes.txt", []byte("not a recording"))

	code, report := h.eplCall("GET", "")
	if code != http.StatusOK || report["found"] != true || report["entries"].(float64) != 2 {
		t.Fatalf("check: %d %v", code, report)
	}
	if lost := report["lost"].([]any); len(lost) != 1 || lost[0] != "lost one.ts" {
		t.Fatalf("lost: %v", report["lost"])
	}
	if d := report["dangling"].([]any); len(d) != 1 || d[0] != "gone.ts" {
		t.Fatalf("dangling: %v", report["dangling"])
	}

	code, out := h.eplCall("POST", "")
	if code != http.StatusOK || out["changed"] != true || out["added"].(float64) != 1 {
		t.Fatalf("rebuild: %d %v", code, out)
	}
	bak, ok := h.ftp.Get("/var/tuxbox/config/enigma/recordings.bak")
	if !ok || string(bak) != eplSample {
		t.Fatalf("the old list was not kept: %q", bak)
	}
	got, _ := h.ftp.Get("/var/tuxbox/config/enigma/recordings.epl")
	if !strings.HasPrefix(string(got), eplSample) || !strings.Contains(string(got), "/hdd/movie/lost one.ts") {
		t.Fatalf("rebuilt list:\n%s", got)
	}
	// Nothing was removed without being asked.
	if !strings.Contains(string(got), "gone.ts") {
		t.Fatal("a dangling entry was removed without prune=1")
	}
	// A second run has nothing left to do.
	if _, again := h.eplCall("POST", ""); again["changed"] != false {
		t.Fatalf("second run: %v", again)
	}
	// Pruning is opt-in.
	if _, pruned := h.eplCall("POST", "?prune=1"); pruned["removed"].(float64) != 1 {
		t.Fatalf("prune: %v", pruned)
	}
	got, _ = h.ftp.Get("/var/tuxbox/config/enigma/recordings.epl")
	if strings.Contains(string(got), "gone.ts") {
		t.Fatal("prune=1 did not remove the dangling entry")
	}
}

func TestRecordingsEPLCreatesAMissingList(t *testing.T) {
	h := newHarness(t)
	h.ftp.MkDir("/hdd/movie")
	h.ftp.MkDir("/var/tuxbox/config/enigma")
	h.ftp.Put("/hdd/movie/a.ts", []byte("x"))
	code, out := h.eplCall("POST", "")
	if code != http.StatusOK || out["backup"] != false {
		t.Fatalf("%d %v", code, out)
	}
	got, ok := h.ftp.Get("/var/tuxbox/config/enigma/recordings.epl")
	if !ok || !strings.HasPrefix(string(got), "#NAME RecMovies\n#SERVICE:") {
		t.Fatalf("new list: %q", got)
	}
}

func TestRecordingsEPLRejectsBadPaths(t *testing.T) {
	h := newHarness(t)
	if code, _ := h.eplCall("GET", "?dir=movie"); code != http.StatusBadRequest {
		t.Fatalf("relative dir accepted: %d", code)
	}
}
