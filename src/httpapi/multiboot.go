package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// bootSlot is one entry of the receiver's MultiBoot list as shown in the power
// dialog. OpenWebif lists a slot once per boot code (for example "4" and "4 (1)"
// on receivers whose image has several kernels), so a slot is identified by the
// pair of slot and boot code, not by the slot alone.
type bootSlot struct {
	Slot    string `json:"slot"`
	Code    string `json:"code"`
	Label   string `json:"label"`
	Current bool   `json:"current"`
}

// OpenWebif (oe-alliance, controllers/MultiBoot.py) serves MultiBoot outside of
// /api: GET /multiboot/get lists the slots and GET /multiboot/set?slot=&bootcode=
// activates one and reboots. Images without MultiBoot support answer 404, and
// images with the plugin but no MultiBoot hardware answer "multiboot": false.
const (
	multibootGetPath = "/multiboot/get"
	multibootSetPath = "/multiboot/set"
	maxBootSlots     = 64
	maxBootText      = 120
)

var (
	bootSlotName = regexp.MustCompile(`^[0-9A-Za-z_-]{1,8}$`)
	bootCodeName = regexp.MustCompile(`^[0-9A-Za-z_-]{0,16}$`)
)

// parseBootSlots reads the answer of /multiboot/get. Entries that do not look
// like a slot are skipped, text is bounded, and nothing but the fields the
// dialog shows is kept.
func parseBootSlots(body []byte) []bootSlot {
	var reply struct {
		MultiBoot bool `json:"multiboot"`
		Slots     []struct {
			Slot      string `json:"slot"`
			BootCode  string `json:"bootCode"`
			Label     string `json:"label"`
			ImageName string `json:"imagename"`
			Current   bool   `json:"current"`
		} `json:"slots"`
	}
	if json.Unmarshal(body, &reply) != nil || !reply.MultiBoot {
		return nil
	}
	var slots []bootSlot
	for _, entry := range reply.Slots {
		if !bootSlotName.MatchString(entry.Slot) || !bootCodeName.MatchString(entry.BootCode) {
			continue
		}
		label := strings.TrimSpace(entry.Label)
		if label == "" {
			label = entry.Slot
		}
		if name := strings.TrimSpace(entry.ImageName); name != "" && name != "Unknown" {
			label += ":" + name
		}
		if len(label) > maxBootText {
			label = label[:maxBootText]
		}
		slots = append(slots, bootSlot{Slot: entry.Slot, Code: entry.BootCode, Label: label, Current: entry.Current})
		if len(slots) == maxBootSlots {
			break
		}
	}
	return slots
}

type httpStatusError int

func (e httpStatusError) Error() string {
	return "The web interface answered HTTP " + strconv.Itoa(int(e)) + "."
}

// fetchBootSlots asks the receiver for its MultiBoot slots. An empty list means
// the receiver (or its web interface) has no MultiBoot support.
func fetchBootSlots(ctx context.Context, web deviceWeb) ([]bootSlot, error) {
	status, body, err := web.getBounded(ctx, multibootGetPath, 128<<10)
	switch {
	case err != nil:
		return nil, err
	case status == http.StatusNotFound:
		return nil, nil
	case status != http.StatusOK:
		return nil, httpStatusError(status)
	}
	return parseBootSlots(body), nil
}

// writeBootFailure answers for a failed MultiBoot request to the receiver.
func writeBootFailure(w http.ResponseWriter, web deviceWeb, err error) {
	if _, isStatus := err.(httpStatusError); isStatus {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error(), "code": "web_error"})
		return
	}
	writeJSON(w, http.StatusBadGateway, map[string]any{"error": web.unreachable(err), "code": "web_unreachable"})
}

// deviceMultiboot lists the receiver's MultiBoot slots for the power dialog.
func (s *Server) deviceMultiboot(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	web := newDeviceWeb(profile, 8*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
	defer cancel()
	slots, err := fetchBootSlots(ctx, web)
	if err != nil {
		writeBootFailure(w, web, err)
		return
	}
	if slots == nil {
		slots = []bootSlot{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"supported": len(slots) > 0, "slots": slots})
}

type multibootRequest struct {
	Slot string `json:"slot"`
	Code string `json:"code"`
}

// deviceMultibootSet restarts the receiver into one of its MultiBoot slots. The
// slot and boot code must be a pair the receiver itself just listed, and it must
// not be the one that is already running.
func (s *Server) deviceMultibootSet(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	var input multibootRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	slot, code := strings.TrimSpace(input.Slot), strings.TrimSpace(input.Code)
	if !bootSlotName.MatchString(slot) || !bootCodeName.MatchString(code) {
		writeError(w, http.StatusBadRequest, "unknown MultiBoot slot")
		return
	}
	web := newDeviceWeb(profile, 10*time.Second)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	slots, err := fetchBootSlots(ctx, web)
	if err != nil {
		writeBootFailure(w, web, err)
		return
	}
	var target *bootSlot
	for i := range slots {
		if slots[i].Slot == slot && slots[i].Code == code {
			target = &slots[i]
		}
	}
	switch {
	case target == nil:
		writeError(w, http.StatusUnprocessableEntity, "the receiver does not list that MultiBoot slot")
		return
	case target.Current:
		writeError(w, http.StatusConflict, "that slot is already running")
		return
	}
	query := url.Values{}
	query.Set("slot", slot)
	query.Set("bootcode", code)
	status, body, err := web.press(ctx, multibootSetPath+"?"+query.Encode())
	if err != nil {
		// A receiver that is restarting may drop the connection before it answers.
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		return
	}
	if writeWebFailure(w, web, status, body, nil) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
