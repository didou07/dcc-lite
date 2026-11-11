package logx

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestHub(t *testing.T, o Options) (*Hub, *bytes.Buffer) {
	t.Helper()
	buf := &bytes.Buffer{}
	o.Console = buf
	h, err := Init(o)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.Close)
	return h, buf
}

func TestLevelsFilterConsoleButMemoryKeepsDebug(t *testing.T) {
	h, buf := newTestHub(t, Options{ConsoleLevel: LevelInfo})
	log := h.For("files")
	log.Debug("hidden on console")
	log.Info("shown", "host", "10.0.0.5")
	out := buf.String()
	if strings.Contains(out, "hidden") || !strings.Contains(out, "shown") {
		t.Fatalf("console filtering wrong:\n%s", out)
	}
	entries, _ := h.Ring().Snapshot(Query{})
	if len(entries) != 2 {
		t.Fatalf("memory should hold debug + info, got %d", len(entries))
	}
	h.SetConsoleLevel(LevelTrace)
	log.Log(nil, LevelTrace, "now visible")
	if !strings.Contains(buf.String(), "now visible") {
		t.Fatal("runtime level change ignored")
	}
}

func TestRedaction(t *testing.T) {
	h, buf := newTestHub(t, Options{})
	log := h.For("auth")
	log.Info("login", "password", "hunter2", "url", "ftp://root:hunter2@192.168.1.5/x", "note", "token=abc123 ok")
	out := buf.String()
	if strings.Contains(out, "hunter2") || strings.Contains(out, "abc123") {
		t.Fatalf("secret leaked:\n%s", out)
	}
}

func TestFaultGroupingAndPanic(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	log := h.For("device")
	for i := 0; i < 5; i++ {
		log.Error("device request failed", "host", "192.168.1.20:80", "error", errors.New("dial tcp 192.168.1.20:80: i/o timeout"), "attempt", i)
	}
	log.Warn("slow response", "ms", 4100)
	func() {
		defer Recover(log, "worker")
		var m map[string]int
		m["x"] = 1
	}()
	faults, st := h.Faults().List()
	if len(faults) != 3 {
		t.Fatalf("want 3 fault groups, got %d: %+v", len(faults), faults)
	}
	var grouped, panicked bool
	for _, f := range faults {
		if f.Count == 5 {
			grouped = true
		}
		if f.Panic && strings.Contains(f.Stack, "logx_test.go") {
			panicked = true
		}
	}
	if !grouped || !panicked || st.Open != 3 || st.Panics != 1 {
		t.Fatalf("grouped=%v panicked=%v stats=%+v", grouped, panicked, st)
	}
	if !h.Faults().Ack(faults[0].ID) {
		t.Fatal("ack failed")
	}
}

func TestRotationAndFreshFaultsPerRun(t *testing.T) {
	dir := t.TempDir()
	h, _ := newTestHub(t, Options{Dir: dir, MaxFileSize: 400, KeepFiles: 2, FileLevel: LevelDebug})
	log := h.For("app")
	for i := 0; i < 40; i++ {
		log.Info("line", "n", i)
	}
	log.Error("boom", "error", "x")
	h.Close()
	for _, name := range []string{"dcclite.log", "dcclite.log.1", "dcclite.log.2", "faults.json"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "dcclite.log.3")); err == nil {
		t.Fatal("kept more rotated files than configured")
	}
	h2, _ := newTestHub(t, Options{Dir: dir})
	if list, _ := h2.Faults().List(); len(list) != 0 {
		t.Fatalf("faults from the previous run were loaded: %d", len(list))
	}
}

func TestQueryFilters(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	h.For("files").Info("a one")
	h.For("term").Warn("b two")
	h.For("files").Error("c three")
	got, _ := h.Ring().Snapshot(Query{MinLevel: levelRank("warn")})
	if len(got) != 2 {
		t.Fatalf("level filter: %d", len(got))
	}
	got, _ = h.Ring().Snapshot(Query{Components: map[string]bool{"files": true}, Text: "THREE"})
	if len(got) != 1 || got[0].Message != "c three" {
		t.Fatalf("component/text filter: %+v", got)
	}
}

func TestPackageLevelLoggerFollowsLaterInit(t *testing.T) {
	early := For("early")
	h, buf := newTestHub(t, Options{})
	early.Info("hello")
	if !strings.Contains(buf.String(), "hello") {
		t.Fatal("early logger did not reach the new hub")
	}
	if got, _ := h.Ring().Snapshot(Query{}); len(got) != 1 {
		t.Fatalf("ring has %d entries", len(got))
	}
}

func TestRecordsStayOnOneLine(t *testing.T) {
	dir := t.TempDir()
	h, buf := newTestHub(t, Options{Dir: dir})
	log := h.For("device")
	log.Error("multi\nline message", "error", errors.New("first\nsecond"), "host", "10.0.0.1")
	func() {
		defer Recover(log, "worker")
		var m map[string]int
		m["x"] = 1
	}()
	h.Close()
	data, err := os.ReadFile(filepath.Join(dir, "dcclite.log"))
	if err != nil {
		t.Fatal(err)
	}
	for name, text := range map[string]string{"console": buf.String(), "file": string(data)} {
		lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
		if len(lines) != 2 {
			t.Fatalf("%s: want 2 lines, got %d:\n%s", name, len(lines), text)
		}
		for _, l := range lines {
			if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
				t.Fatalf("%s: indented line %q", name, l)
			}
		}
	}
	if !strings.Contains(string(data), "stack=") {
		t.Fatalf("file line lost the stack:\n%s", data)
	}
}

func TestReportKeepsOneFaultPerLine(t *testing.T) {
	h, _ := newTestHub(t, Options{})
	log := h.For("device")
	for i := 0; i < 3; i++ {
		log.Error("request failed", "host", "10.0.0.1", "error", errors.New("timeout"))
	}
	report := h.Report(map[string]string{"version": "1"}, Query{})
	if !strings.Contains(report, "faults open=1") || !strings.Contains(report, "x3") || !strings.Contains(report, "error=timeout") {
		t.Fatalf("report incomplete:\n%s", report)
	}
	for _, l := range strings.Split(strings.TrimRight(report, "\n"), "\n") {
		if strings.HasPrefix(l, " ") || strings.HasPrefix(l, "\t") {
			t.Fatalf("indented report line %q", l)
		}
	}
}
