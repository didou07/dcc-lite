package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/didou07/dcc-lite/src/netscan"
)

// networkRanges suggests scan ranges from this computer's private interfaces.
func (s *Server) networkRanges(w http.ResponseWriter, _ *http.Request) {
	ranges := netscan.SuggestRanges()
	if ranges == nil {
		ranges = []string{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ranges": ranges})
}

// networkScan looks for receivers (open FTP / Telnet ports) in a private address range.
func (s *Server) networkScan(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Range string `json:"range"`
	}
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	addrs, err := netscan.ParseRange(input.Range)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if !s.scanning.CompareAndSwap(false, true) {
		writeError(w, http.StatusTooManyRequests, "another scan is already running")
		return
	}
	defer s.scanning.Store(false)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	started := time.Now()
	hosts := netscan.Scan(ctx, addrs, netscan.Options{})
	if hosts == nil {
		hosts = []netscan.Host{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"hosts": hosts, "scanned": len(addrs), "duration_ms": time.Since(started).Milliseconds(),
		"complete": ctx.Err() == nil,
	})
}
