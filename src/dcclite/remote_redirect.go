package main

import (
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
)

var dnsLabelPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?$`)

func validPublicDNSName(host string) bool {
	host = strings.TrimSuffix(strings.TrimSpace(host), ".")
	if len(host) < 4 || len(host) > 253 || strings.Contains(host, "..") {
		return false
	}
	if strings.EqualFold(host, "localhost") || !strings.Contains(host, ".") || net.ParseIP(host) != nil {
		return false
	}
	for _, suffix := range []string{".local", ".localhost", ".lan", ".home", ".internal", ".test", ".invalid", ".example", ".home.arpa", ".localdomain"} {
		if strings.HasSuffix(strings.ToLower(host), suffix) {
			return false
		}
	}
	for _, label := range strings.Split(host, ".") {
		if !dnsLabelPattern.MatchString(label) {
			return false
		}
	}
	return true
}

func remoteHTTPListenAddress(httpsAddress string) (string, error) {
	host, port, err := net.SplitHostPort(httpsAddress)
	if err != nil {
		return "", err
	}
	if port == "80" {
		return "", errors.New("the HTTPS listener cannot use port 80 because that port is reserved for HTTP redirects and ACME validation")
	}
	return net.JoinHostPort(host, "80"), nil
}

func remoteURLHost(host, httpsPort string) string {
	if ip := net.ParseIP(strings.Trim(host, "[]")); ip != nil && strings.Contains(host, ":") {
		host = "[" + strings.Trim(host, "[]") + "]"
	}
	if httpsPort != "443" {
		return net.JoinHostPort(strings.Trim(host, "[]"), httpsPort)
	}
	return host
}

func newHTTPSRedirectHandler(hosts []string, httpsPort string) http.Handler {
	allowed := make(map[string]struct{}, len(hosts))
	for _, host := range hosts {
		host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(strings.Trim(host, "[]"))), ".")
		if host != "" {
			allowed[host] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := strings.TrimSpace(r.Host)
		if parsed, _, err := net.SplitHostPort(host); err == nil {
			host = parsed
		}
		host = strings.TrimSuffix(strings.ToLower(strings.Trim(host, "[]")), ".")
		if _, ok := allowed[host]; !ok {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodPost && r.Method != http.MethodPut && r.Method != http.MethodPatch && r.Method != http.MethodDelete && r.Method != http.MethodOptions {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		target := &url.URL{
			Scheme:   "https",
			Host:     remoteURLHost(host, httpsPort),
			Path:     r.URL.Path,
			RawPath:  r.URL.RawPath,
			RawQuery: r.URL.RawQuery,
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Referrer-Policy", "no-referrer")
		http.Redirect(w, r, target.String(), http.StatusPermanentRedirect)
	})
}
