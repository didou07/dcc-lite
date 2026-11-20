package httpapi

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	grabAddressTimeout   = 30 * time.Second
	freezeFrame4KTimeout = 45 * time.Second

	screenReplyEmpty  = "empty"
	screenReplyBusy   = "busy"
	screenReplyNoFile = "no_file"
	screenReplyOther  = "other"
)

var noFileReply = regexp.MustCompile(`^/tmp/[^\s<>]+\.(?:jpg|png|bmp)\b`)

// screenAddressTimeout is the time one screenshot address may take.
func screenAddressTimeout(addr string) time.Duration {
	if strings.HasPrefix(addr, "/freezeframe") && strings.Contains(addr, "resolution=3840x2160") {
		return freezeFrame4KTimeout
	}
	return grabAddressTimeout
}

func classifyScreenReply(body []byte) (kind, evidence string) {
	evidence = screenEvidence(body)
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return screenReplyEmpty, evidence
	}
	lower := bytes.ToLower(body)
	if bytes.Contains(lower, []byte(`name="screenshot"`)) && bytes.Contains(lower, []byte(`name="grab"`)) {
		return screenReplyBusy, evidence
	}
	if len(trimmed) < 256 && noFileReply.Match(trimmed) {
		return screenReplyNoFile, evidence
	}
	return screenReplyOther, evidence
}

// screenEvidence is the start of a reply made safe for a log line.
func screenEvidence(body []byte) string {
	const keep = 120
	if len(body) > keep {
		body = body[:keep]
	}
	out := make([]byte, 0, len(body))
	for _, b := range body {
		switch {
		case b >= 32 && b < 127:
			out = append(out, b)
		case b == '\n' || b == '\r' || b == '\t':
			out = append(out, ' ')
		default:
			out = append(out, '.')
		}
	}
	return strings.TrimSpace(string(out))
}

func screenKindMessage(kind string) string {
	switch kind {
	case screenReplyBusy:
		return "The Freeze Frame plug-in is still busy with an earlier request."
	case screenReplyNoFile:
		return "The Freeze Frame plug-in ran, but its capture command made no picture file."
	case screenReplyEmpty:
		return "The receiver answered with an empty reply."
	}
	return "The receiver did not return a picture."
}

type e2VideoService struct {
	Name   string `xml:"e2servicename"`
	Width  string `xml:"e2videowidth"`
	Height string `xml:"e2videoheight"`
}

func receiverVideo(web deviceWeb) (name, rawWidth, rawHeight string, width int) {
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	var cur struct {
		Service e2VideoService `xml:"e2service"`
	}
	svc := e2VideoService{}
	if err := web.legacyXML(ctx, "/web/getcurrent", 1<<20, &cur); err == nil {
		svc = cur.Service
	}
	if strings.TrimSpace(svc.Width) == "" {
		var about struct {
			Service e2VideoService `xml:"e2about>e2service"`
		}
		if err := web.legacyXML(ctx, "/web/about", 1<<20, &about); err == nil {
			svc = about.Service
		}
	}
	return strings.TrimSpace(svc.Name), strings.TrimSpace(svc.Width), strings.TrimSpace(svc.Height), parseVideoDimension(svc.Width)
}

func parseVideoDimension(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return 0
	}
	if strings.HasPrefix(s, "0x") {
		if n, err := strconv.ParseInt(s[2:], 16, 32); err == nil {
			return int(n)
		}
		return 0
	}
	if !strings.ContainsAny(s, "abcdef") {
		if n, err := strconv.Atoi(s); err == nil {
			return n
		}
		return 0
	}
	if n, err := strconv.ParseInt(s, 16, 32); err == nil {
		return int(n)
	}
	return 0
}

func logScreenVideo(web deviceWeb) {
	name, rawW, rawH, w := receiverVideo(web)
	if rawW == "" && rawH == "" {
		return
	}
	devLog.Warn("screenshots failed; the receiver is decoding", "base", web.base, "channel", name, "width", rawW, "height", rawH, "width_px", w)
}

// screenDiagResult is one address tried by the diagnosis.
type screenDiagResult struct {
	Address     string `json:"address"`
	Status      int    `json:"status,omitempty"`
	ContentType string `json:"content_type,omitempty"`
	Bytes       int    `json:"bytes"`
	Millis      int64  `json:"ms"`
	Outcome     string `json:"outcome"`
	Evidence    string `json:"evidence,omitempty"`
	Error       string `json:"error,omitempty"`
}

func (s *Server) deviceScreenDiag(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.app.Profiles.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "device profile not found")
		return
	}
	if !profile.Enabled {
		writeError(w, http.StatusConflict, "device profile is disabled")
		return
	}
	mode := r.URL.Query().Get("mode")
	switch mode {
	case "all", "video", "osd":
	case "":
		mode = "all"
	default:
		writeError(w, http.StatusBadRequest, "mode must be all, video or osd")
		return
	}
	width := 1280
	if raw := r.URL.Query().Get("width"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || !screenWidths[n] {
			writeError(w, http.StatusBadRequest, "width must be 320, 480, 640, 720, 960, 1280, 1920 or 3840")
			return
		}
		width = n
	}
	wait := 30 * time.Second
	if raw := r.URL.Query().Get("wait"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 5 || n > 90 {
			writeError(w, http.StatusBadRequest, "wait must be 5 to 90 seconds")
			return
		}
		wait = time.Duration(n) * time.Second
	}
	stopAtFirst := r.URL.Query().Get("first") == "1"

	web := newDeviceWeb(profile, 100*time.Second)
	st := grabStateFor(web.base)
	select {
	case st.gate <- struct{}{}:
	case <-r.Context().Done():
		return
	}
	defer func() {
		st.mu.Lock()
		st.doneAt = time.Now()
		st.mu.Unlock()
		<-st.gate
	}()

	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Minute)
	defer cancel()

	name, rawW, rawH, videoW := receiverVideo(web)
	addresses := grabAddresses(mode, width, web.kind)
	results := make([]screenDiagResult, 0, len(addresses))
	pluginStuck := false
	for _, addr := range addresses {
		res := screenDiagResult{Address: addr}
		if strings.HasPrefix(addr, "/freezeframe") && pluginStuck {
			res.Outcome = "skipped"
			res.Error = "an earlier Freeze Frame address did not finish, so the plug-in would only answer busy"
			results = append(results, res)
			continue
		}
		actx, acancel := context.WithTimeout(ctx, wait)
		began := time.Now()
		resp, err := web.get(actx, addr)
		if err != nil {
			acancel()
			res.Millis = time.Since(began).Milliseconds()
			if errors.Is(err, context.DeadlineExceeded) {
				res.Outcome = "timeout"
				res.Error = "no answer within " + strconv.Itoa(int(wait.Seconds())) + " s"
				if strings.HasPrefix(addr, "/freezeframe") {
					pluginStuck = true
				}
			} else {
				res.Outcome = "error"
				res.Error = web.unreachable(err)
			}
			results = append(results, res)
			continue
		}
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxScreenBytes+1))
		resp.Body.Close()
		acancel()
		res.Millis = time.Since(began).Milliseconds()
		res.Status = resp.StatusCode
		res.ContentType = resp.Header.Get("Content-Type")
		res.Bytes = len(body)
		switch {
		case rerr != nil:
			res.Outcome = "read_error"
			res.Error = rerr.Error()
		case resp.StatusCode != http.StatusOK:
			res.Outcome = "http_" + strconv.Itoa(resp.StatusCode)
			res.Evidence = screenEvidence(body)
		case isPicture(body):
			res.Outcome = "picture"
		default:
			res.Outcome, res.Evidence = classifyScreenReply(body)
			if res.Outcome == screenReplyBusy && strings.HasPrefix(addr, "/freezeframe") {
				pluginStuck = true
			}
		}
		results = append(results, res)
		if stopAtFirst && res.Outcome == "picture" {
			break
		}
	}
	if web.kind == "dreamos" {
		foundPicture := false
		for _, result := range results {
			if result.Outcome == "picture" {
				foundPicture = true
				break
			}
		}
		if !foundPicture {
			began := time.Now()
			body, err := captureDreamOSWithCommand(ctx, web, mode, width)
			res := screenDiagResult{Address: "DreamOS command screenshot via Telnet and FTP", Millis: time.Since(began).Milliseconds()}
			if err != nil {
				res.Outcome = "error"
				res.Error = cleanText(err.Error(), 240)
			} else {
				res.Outcome = "picture"
				res.Bytes = len(body)
				res.ContentType = "image/jpeg"
			}
			results = append(results, res)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"kind":    web.kind,
		"mode":    mode,
		"width":   width,
		"video":   map[string]any{"channel": name, "width_raw": rawW, "height_raw": rawH, "width_px": videoW},
		"results": results,
		"notes":   screenDiagNotes(results, videoW),
	})
}

func screenDiagNotes(results []screenDiagResult, videoWidth int) []string {
	var notes []string
	seen := map[string]bool{}
	add := func(s string) {
		if !seen[s] {
			seen[s] = true
			notes = append(notes, s)
		}
	}
	anyPicture := false
	for _, r := range results {
		switch r.Outcome {
		case "picture":
			anyPicture = true
		case screenReplyBusy:
			add("busy: the Freeze Frame plug-in is still running an earlier request (it keeps one running flag and answers its HTML form until that request ends). If this stays, a command on the receiver is stuck: run `ps | grep [f]fmpeg` and `killall ffmpeg` in the terminal.")
		case screenReplyNoFile:
			add("no_file: the plug-in ran its capture command and no picture file appeared. The plug-in does not report why. Run the same command by hand: for a picture wider than 1920 it uses `/usr/bin/ffmpeg` (check `ls -l /usr/bin/ffmpeg`), otherwise `dreamboxctl screenshot` on arm64 or `/usr/bin/grab` on dm900/dm920/dm7080/dm820/dm520.")
		case screenReplyEmpty:
			add("empty: the address answered 200 with no data. A known case on some images is /grab with the aio-grab tool; /screenshot is the address that comes from enigma2 itself.")
		case "timeout":
			add("timeout: the address did not answer in time. Compare with the `ms` of the addresses that did answer, and run again with a larger `wait` to see whether it answers at all.")
		case "error":
			if strings.Contains(r.Address, "DreamOS command screenshot") {
				add("DreamOS command fallback failed: this capture path needs a working Telnet shell, dreamboxctl or grab on the receiver, and FTP access to retrieve the temporary image; inspect the error field for the failing step.")
			}
		case "http_404", "http_405":
			add("404/405: the address does not exist on this receiver (for /freezeframe: the plug-in is not installed or the web interface was not restarted after installing it).")
		case "http_500":
			add("500: the receiver's web interface failed while making the picture; `evidence` holds what it said.")
		}
	}
	if videoWidth > 1920 {
		add("the receiver is decoding a picture wider than 1920. For video or video+OSD with method auto, the plug-in then does not use grab or dreamboxctl: it reads the stream from localhost:8001 with /usr/bin/ffmpeg (OSD alone still uses the normal tool).")
	}
	if !anyPicture && len(results) > 0 {
		add("no address returned a picture; the `outcome` of each address above is the evidence to send.")
	}
	return notes
}
