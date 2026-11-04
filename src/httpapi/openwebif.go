package httpapi

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/didou07/dcc-lite/src/config"
)

// deviceWeb talks to the web interface (OpenWebif) of one saved device. The
// address always comes from the saved profile, never from the browser.
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
	// Port 80 is plain HTTP and 443 is HTTPS on every receiver. A saved 80 with the
	// HTTPS box ticked (or 443 without it) is a leftover of changing only one of the
	// two, and would wait for a TLS handshake that never comes, so follow the scheme.
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
		// The device profile opts in to the self-signed certificates receivers use.
		// Many receivers run old TLS stacks (TLS 1.0/1.1, RSA key exchange only, CBC
		// ciphers) that Go's defaults refuse, so a handshake failed although the
		// receiver was reachable. This client only ever talks to the saved device.
		// A new transport is made per request, so keep-alive is off to not leak sockets.
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
			TLSClientConfig:     &tls.Config{InsecureSkipVerify: true, MinVersion: tls.VersionTLS10, CipherSuites: suites}, //nolint:gosec
		}
	}
	return deviceWeb{
		base: scheme + "://" + net.JoinHostPort(strings.Trim(profile.Host, "[]"), strconv.Itoa(port)),
		port: port, user: user, pass: profile.Password, client: client,
	}
}

// get requests a path (with query) on the device's web interface.
func (d deviceWeb) get(ctx context.Context, pathAndQuery string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.base+pathAndQuery, nil)
	if err != nil {
		return nil, err
	}
	if d.pass != "" {
		req.SetBasicAuth(d.user, d.pass)
	}
	return d.client.Do(req)
}

// unreachable words the error shown when the device's web interface could not
// be reached, including the real reason and the most likely fix.
func (d deviceWeb) unreachable(err error) string {
	message := "The receiver's web interface did not answer at " + d.base + "."
	text := ""
	if err != nil {
		text = err.Error()
	}
	lower := strings.ToLower(text)
	switch {
	case strings.Contains(lower, "http response to https client") || strings.Contains(lower, "first record does not look like a tls handshake"):
		message += " This port speaks plain HTTP: untick “The receiver's web interface uses HTTPS” in the device settings, or use the receiver's HTTPS port."
	case strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timeout"):
		message += " It did not reply in time. If the HTTPS box is ticked the port may be plain HTTP (usually 80), and if it is not ticked the port may be HTTPS (usually 443). Also check that the receiver is on and reachable."
	case strings.Contains(lower, "tls:") || strings.Contains(lower, "certificate") || strings.Contains(lower, "handshake"):
		message += " The secure connection could not be set up. Check the web port, or untick the HTTPS box if the receiver also answers on plain HTTP."
	case strings.Contains(lower, "refused"):
		message += " Nothing is listening on that port. Check the web port and the HTTPS box in the device settings, and that OpenWebif is installed."
	default:
		message += " Check the web port and the HTTPS box in the device settings, and that OpenWebif is installed."
	}
	if text != "" {
		if len(text) > 200 {
			text = text[:200]
		}
		message += " (" + text + ")"
	}
	return message
}
