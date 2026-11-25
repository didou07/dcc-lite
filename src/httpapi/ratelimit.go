package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/didou07/dcc-lite/src/filetransfer"
)

func (s *Server) getTransferRateLimit(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"kbps": filetransfer.RateLimitKBps()})
}

func (s *Server) setTransferRateLimit(w http.ResponseWriter, r *http.Request) {
	var input struct {
		KBps int64 `json:"kbps"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 1024)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if input.KBps < 0 || input.KBps > filetransfer.MaxRateLimitKBps {
		writeError(w, http.StatusBadRequest, "kbps must be between 0 and 102400")
		return
	}
	filetransfer.SetRateLimitKBps(input.KBps)
	writeJSON(w, http.StatusOK, map[string]any{"kbps": filetransfer.RateLimitKBps()})
}
