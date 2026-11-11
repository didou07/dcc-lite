package httpapi

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/didou07/dcc-lite/src/config"
)

type deviceWeb struct {
	base   string
	port   int
	user   string
	pass   string
	client *http.Client
}

func newDeviceWeb(profile config.Profile, timeout time.Duration) deviceWeb {
	scheme := "http"
	if profile.Scheme == "https" {
		scheme = "https"
	}
	port := profile.Port

	switch {
	case port == 0 && scheme == "https", port == 80 && scheme == "https":
		port = 443
	case port == 0, port == 443 && scheme == "http":
		port = 80
	}
	user := strings.TrimSpace(profile.Username)
	if user == "" {
		user = config.DefaultUsername
	}
	client := &http.Client{Timeout: timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	if scheme == "https" && profile.InsecureTLS {

		var suites []uint16
		for _, suite := range tls.CipherSuites() {
			suites = append(suites, suite.ID)
		}
		for _, suite := range tls.InsecureCipherSuites() {
			suites = append(suites, suite.ID)
		}
		client.Transport = &http.Transport{
			TLSHandshakeTimeout: timeout,
			DisableKeepAlives:   true,
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10, CipherSuites: suites},
		}
	}
	return deviceWeb{
		base: scheme + "://" + net.JoinHostPort(strings.Trim(profile.Host, "[]"), strconv.Itoa(port)),
		port: port, user: user, pass: profile.Password, client: client,
	}
}

const maxWebRequestsPerDevice = 2

var webSlots sync.Map

func webSlotsFor(base string) chan struct{} {
	if slots, ok := webSlots.Load(base); ok {
		return slots.(chan struct{})
	}
	slots, _ := webSlots.LoadOrStore(base, make(chan struct{}, maxWebRequestsPerDevice))
	return slots.(chan struct{})
}

type slotBody struct {
	io.ReadCloser
	once    sync.Once
	release func()
}

func (b *slotBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(b.release)
	return err
}

func (d deviceWeb) get(ctx context.Context, pathAndQuery string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+pathAndQuery, nil)
	if err != nil {
		return nil, err
	}
	if d.pass != "" {
		req.SetBasicAuth(d.user, d.pass)
	}
	if err := webCooldownError(d.base); err != nil {
		return nil, err
	}
	slots := webSlotsFor(d.base)
	select {
	case slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	release := func() { <-slots }
	resp, err := d.client.Do(req)
	if err != nil {
		release()
		webNoteFailure(d.base, err)
		return nil, err
	}
	webNoteSuccess(d.base)
	resp.Body = &slotBody{ReadCloser: resp.Body, release: release}
	return resp, nil
}

type webFailure struct {
	until  time.Time
	err    error
	streak int
}

var webFailures sync.Map
var webFailuresMu sync.Mutex

func errKind(err error) string {
	if err == nil {
		return ""
	}
	lower := strings.ToLower(err.Error())
	switch {
	case strings.Contains(lower, "http response to https client") || strings.Contains(lower, "first record does not look like a tls handshake"):
		return "plainhttp"
	case strings.Contains(lower, "refused"):
		return "refused"
	case strings.Contains(lower, "reset by peer") || strings.Contains(lower, "broken pipe") || strings.HasSuffix(lower, ": eof") || lower == "eof" || strings.Contains(lower, "unexpected eof"):
		return "reset"
	case strings.Contains(lower, "no route to host") || strings.Contains(lower, "network is unreachable") || strings.Contains(lower, "no such host"):
		return "noroute"
	case strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timeout"):
		return "timeout"
	case strings.Contains(lower, "tls:") || strings.Contains(lower, "certificate") || strings.Contains(lower, "handshake"):
		return "tls"
	}
	return "other"
}

func webNoteFailure(base string, err error) {
	kind := errKind(err)
	if kind != "refused" && kind != "reset" {
		return
	}
	webFailuresMu.Lock()
	defer webFailuresMu.Unlock()
	streak := 0
	if v, ok := webFailures.Load(base); ok {
		streak = v.(*webFailure).streak
	}
	streak++
	pause := time.Duration(streak) * 3 * time.Second
	if pause > 15*time.Second {
		pause = 15 * time.Second
	}
	webFailures.Store(base, &webFailure{until: time.Now().Add(pause), err: err, streak: streak})
}

func webNoteSuccess(base string) {
	webFailuresMu.Lock()
	webFailures.Delete(base)
	webFailuresMu.Unlock()
}

func webCooldownError(base string) error {
	webFailuresMu.Lock()
	defer webFailuresMu.Unlock()
	if v, ok := webFailures.Load(base); ok {
		if f := v.(*webFailure); time.Now().Before(f.until) {
			return f.err
		}
	}
	return nil
}

type probeResult struct {
	at       time.Time
	telnet   bool
	ftp      bool
	webPorts []int
}

var probeCache sync.Map

func portOpen(host string, port int) bool {
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(host, strconv.Itoa(port)), 1200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (d deviceWeb) probe() probeResult {
	host, _, _ := net.SplitHostPort(strings.TrimPrefix(strings.TrimPrefix(d.base, "http://"), "https://"))
	if v, ok := probeCache.Load(host); ok && time.Since(v.(probeResult).at) < 5*time.Second {
		return v.(probeResult)
	}
	res := probeResult{at: time.Now()}
	var wg sync.WaitGroup
	var mu sync.Mutex
	check := func(port int, set func()) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if portOpen(host, port) {
				mu.Lock()
				set()
				mu.Unlock()
			}
		}()
	}
	check(23, func() { res.telnet = true })
	check(21, func() { res.ftp = true })
	for _, port := range []int{80, 8080, 443} {
		if port != d.port {
			p := port
			check(p, func() { res.webPorts = append(res.webPorts, p) })
		}
	}
	wg.Wait()
	sort.Ints(res.webPorts)
	probeCache.Store(host, res)
	return res
}

func (d deviceWeb) unreachable(err error) string {
	at := strings.TrimPrefix(strings.TrimPrefix(d.base, "http://"), "https://")
	switch errKind(err) {
	case "plainhttp":
		return "Port " + strconv.Itoa(d.port) + " speaks plain HTTP: untick the HTTPS box in the device settings, or use the receiver's HTTPS port (https://)."
	case "tls":
		return "The secure connection to " + at + " failed. Check the web port, or untick the HTTPS box if the receiver also answers on plain HTTP."
	case "noroute":
		return "Cannot reach " + at + ". Check that the receiver is on and on the same network."
	case "timeout":
		return "The receiver at " + at + " did not reply in time. It may be busy, or the HTTPS box does not match the port (usually 80 for HTTP, 443 for HTTPS)."
	case "refused", "reset":
		p := d.probe()
		if len(p.webPorts) > 0 {
			return "Nothing answers on port " + strconv.Itoa(d.port) + ", but the web interface seems to be on port " + strconv.Itoa(p.webPorts[0]) + ". Change the web port in the device settings."
		}
		if p.telnet || p.ftp {
			if errKind(err) == "reset" {
				return "The receiver is on, but Enigma2's web interface is busy or restarting. Wait a few seconds and try again."
			}
			return "The receiver is on, but Enigma2's web interface is not answering (Enigma2 is probably restarting or crashed). Wait a minute and try again, or restart Enigma2."
		}
		return "The receiver at " + at + " is not answering. Check that it is on and on the same network."
	}
	return "The receiver's web interface at " + at + " did not answer. Check the web port and the HTTPS box in the device settings."
}
