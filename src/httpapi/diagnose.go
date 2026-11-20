package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/filetransfer"
)

type diagnoseCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	State  string `json:"state"` // ok | warn | fail | skip
	Detail string `json:"detail,omitempty"`
	Hint   string `json:"hint,omitempty"`
	MS     int64  `json:"ms,omitempty"`
}

func (s *Server) deviceDiagnose(w http.ResponseWriter, r *http.Request) {
	profile, ok := s.enabledProfile(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()

	var wg sync.WaitGroup
	var web []diagnoseCheck
	var ftp, telnet diagnoseCheck
	wg.Add(3)
	go func() { defer wg.Done(); web = diagnoseWeb(ctx, newDeviceWeb(profile, 12*time.Second)) }()
	go func() {
		defer wg.Done()
		ftp = diagnoseFTP(ctx, profile.Host, profile.EffectiveFTPPort(), profile.Username, profile.Password)
	}()
	go func() {
		defer wg.Done()
		telnet = diagnoseTelnet(ctx, profile.Host, profile.EffectiveTelnetPort())
	}()
	wg.Wait()

	checks := append(web, ftp, telnet)
	failed := 0
	for _, c := range checks {
		if c.State == "fail" {
			failed++
		}
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, map[string]any{"checks": checks, "failed": failed, "checked_at": time.Now().UTC()})
}

func timed(c *diagnoseCheck, began time.Time) { c.MS = time.Since(began).Milliseconds() }

func diagnoseWeb(ctx context.Context, web deviceWeb) []diagnoseCheck {
	iface := diagnoseCheck{ID: "web", Label: "Web interface"}
	channels := diagnoseCheck{ID: "channels", Label: "Channel list"}
	image := diagnoseCheck{ID: "image", Label: "Receiver picture"}

	began := time.Now()
	status, body, err := web.getBounded(ctx, "/api/about", 1<<20)
	timed(&iface, began)
	var about map[string]any
	switch {
	case err != nil:
		iface.State, iface.Detail = "fail", web.unreachable(err)
		iface.Hint = "Check the address and web port."
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		iface.State, iface.Detail = "fail", "The receiver rejected the saved user name or password."
		iface.Hint = "Check the web user name and password."
	case status == http.StatusPreconditionFailed:
		iface.State, iface.Detail = "fail", fmt.Sprintf("HTTP %d: the web interface wants a session id.", status)
		iface.Hint = "Set the web interface type to DreamOS."
	case status != http.StatusOK:
		iface.State, iface.Detail = "fail", fmt.Sprintf("The web interface answered HTTP %d.", status)
		if web.kind == "dreamos" {
			iface.Hint = "Check that the DreamOS is running on this port."
		} else {
			iface.Hint = "Check that OpenWebif is running on this port."
		}
	default:
		if json.Unmarshal(body, &about) != nil || about == nil {
			iface.State, iface.Detail = "fail", "The answer was not a web interface reply."
			iface.Hint = "Another service may use this port."
		} else {
			iface.State = "ok"
			iface.Detail = joinWords(jsonText(asMap(about["info"])["brand"]), jsonText(asMap(about["info"])["model"]))
		}
	}
	if iface.State != "ok" {
		for _, c := range []*diagnoseCheck{&channels, &image} {
			c.State, c.Detail = "skip", "Skipped because the web interface does not answer."
		}
		return []diagnoseCheck{iface, channels, image}
	}

	began = time.Now()
	status, body, err = web.getBounded(ctx, "/api/getallservices", maxChannelListBytes)
	timed(&channels, began)
	var list struct {
		Services []struct {
			Sub []json.RawMessage `json:"subservices"`
		} `json:"services"`
	}
	switch {
	case err != nil:
		channels.State, channels.Detail = "fail", web.unreachable(err)
		channels.Hint = "Try again in a moment."
	case status != http.StatusOK:
		channels.State, channels.Detail = "fail", fmt.Sprintf("The receiver answered HTTP %d.", status)
	case json.Unmarshal(body, &list) != nil:
		channels.State, channels.Detail = "fail", "The receiver did not return a channel list."
		channels.Hint = "Check the receiver's channel list."
	case len(list.Services) == 0:
		channels.State, channels.Detail = "warn", "The channel list is empty."
		channels.Hint = "Scan for channels or restore a backup."
	default:
		total := 0
		for _, b := range list.Services {
			total += len(b.Sub)
		}
		channels.State, channels.Detail = "ok", fmt.Sprintf("%d bouquets, %d channels", len(list.Services), total)
	}

	began = time.Now()
	found := false
	var imgErr error
	for _, path := range boxImageCandidates(about) {
		st, img, e := web.getBounded(ctx, path, maxBoxImageBytes)
		if e != nil {
			imgErr = e
			break
		}
		if st == http.StatusOK && isBoxImage(img) != "" {
			found = true
			break
		}
	}
	timed(&image, began)
	switch {
	case found:
		image.State, image.Detail = "ok", "The receiver picture was found."
	case imgErr != nil:
		image.State, image.Detail = "warn", web.unreachable(imgErr)
	default:
		image.State, image.Detail = "warn", "This receiver has no picture in its web interface."
		image.Hint = "This is cosmetic only; everything else works without it."
	}
	return []diagnoseCheck{iface, channels, image}
}

func diagnoseFTP(ctx context.Context, host string, port int, user, pass string) diagnoseCheck {
	c := diagnoseCheck{ID: "ftp", Label: "FTP (Files)"}
	began := time.Now()
	fctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	client, err := filetransfer.OpenFTP(fctx, filetransfer.FTPOptions{Host: host, Port: port, Username: user, Password: pass})
	timed(&c, began)
	if err != nil {
		c.State = "fail"
		msg := err.Error()
		switch {
		case strings.Contains(msg, "login failed"):
			c.Detail = "FTP is running but the user name or password was refused."
			c.Hint = "Check the FTP user name and password."
		case strings.Contains(msg, "refused"):
			c.Detail = "Nothing is listening on FTP port " + strconv.Itoa(port) + "."
			c.Hint = "Enable FTP on the receiver or fix the port."
		case errors.Is(err, context.DeadlineExceeded) || strings.Contains(msg, "timeout") || strings.Contains(msg, "timed out"):
			c.Detail = "FTP did not answer in time."
			c.Hint = "Check the network and firewall."
		default:
			c.Detail = msg
		}
		return c
	}
	_ = client.Close()
	c.State, c.Detail = "ok", "Logged in."
	return c
}

func diagnoseTelnet(ctx context.Context, host string, port int) diagnoseCheck {
	c := diagnoseCheck{ID: "telnet", Label: "Telnet (Terminal)"}
	began := time.Now()
	dialer := net.Dialer{Timeout: 6 * time.Second}
	conn, err := dialer.DialContext(ctx, "tcp", net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port)))
	if err != nil {
		timed(&c, began)
		c.State = "fail"
		if strings.Contains(strings.ToLower(err.Error()), "refused") {
			c.Detail = "Nothing is listening on Telnet port " + strconv.Itoa(port) + "."
			c.Hint = "Enable Telnet on the receiver or fix the port."
		} else {
			c.Detail = "Telnet is not reachable."
			c.Hint = "Check the network and the Telnet port in the device settings."
		}
		return c
	}
	defer conn.Close()
	_ = conn.SetReadDeadline(time.Now().Add(2500 * time.Millisecond))
	buf := make([]byte, 256)
	n, _ := conn.Read(buf)
	timed(&c, began)
	if n > 0 {
		c.State, c.Detail = "ok", "Telnet answered."
	} else {
		c.State, c.Detail = "warn", "The port is open but the receiver sent nothing yet."
		c.Hint = "Usually fine; the terminal may still work."
	}
	return c
}
