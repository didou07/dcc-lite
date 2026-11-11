package logx

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func (h *Hub) Report(info map[string]string, q Query) string {
	entries, _ := h.ring.Snapshot(q)
	faults, st := h.faults.List()

	keys := make([]string, 0, len(info))
	for k := range info {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	system := make([]string, 0, len(keys)+1)
	for _, k := range keys {
		system = append(system, token(k, info[k]))
	}
	system = append(system, token("uptime", time.Since(h.started).Round(time.Second).String()))

	var b strings.Builder
	b.WriteString("DCC Lite report " + time.Now().Format("2006-01-02 15:04:05 MST") + "\n")
	b.WriteString("system " + strings.Join(system, " ") + "\n")
	b.WriteString("levels " + token("console", LevelName(h.ConsoleLevel())) + " " + token("file", LevelName(h.FileLevel())) + "\n")
	fmt.Fprintf(&b, "faults open=%d errors=%d panics=%d total=%d\n", st.Open, st.Errors, st.Panics, st.Total)
	for _, f := range faults {
		if !f.Acked {
			b.WriteString(faultLine(f))
		}
	}
	b.WriteString("log\n")
	for _, e := range entries {
		b.WriteString(formatLine(e, true))
	}
	return redactHome(b.String())
}

func faultLine(f Fault) string {
	parts := []string{
		"[" + strings.ToUpper(f.Level) + "]",
		padRight(f.Component, componentWidth),
		fmt.Sprintf("x%d", f.Count),
		oneLine(f.Message),
		token("id", f.ID),
		token("first", f.First.Format(timeLayoutFile)),
		token("last", f.Last.Format(timeLayoutFile)),
	}
	if f.Error != "" {
		parts = append(parts, token("error", f.Error))
	}
	if f.Panic {
		parts = append(parts, "panic=true")
	}
	for _, c := range f.Context {
		parts = append(parts, token(c.Key, c.Value))
	}
	if f.Stack != "" {
		parts = append(parts, token("stack", compactStack(f.Stack)))
	}
	return strings.Join(parts, " ") + "\n"
}
