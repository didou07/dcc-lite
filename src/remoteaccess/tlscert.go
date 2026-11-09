package remoteaccess

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	certValidity  = 365 * 24 * time.Hour
	certRenewWhen = 30 * 24 * time.Hour
)

type Certificate struct {
	TLS         tls.Certificate
	Fingerprint string
	Source      string
	Hosts       []string
}

func LoadProvided(certFile, keyFile string) (Certificate, error) {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return Certificate{}, err
	}
	return describe(pair, "provided")
}

func LoadOrCreateSelfSigned(dir string, hosts []string) (Certificate, error) {
	hosts = normalizeHosts(hosts)
	certPath := filepath.Join(dir, "tls", "cert.pem")
	keyPath := filepath.Join(dir, "tls", "key.pem")
	if pair, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil {
		if leaf, err := x509.ParseCertificate(pair.Certificate[0]); err == nil && certUsable(leaf, hosts) {
			pair.Leaf = leaf
			return describe(pair, "self-signed")
		}
	}
	if err := os.MkdirAll(filepath.Dir(certPath), 0o700); err != nil {
		return Certificate{}, err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	if err != nil {
		return Certificate{}, err
	}
	now := time.Now()
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "DCC Lite", Organization: []string{"DCC Lite (self-signed)"}},
		NotBefore:    now.Add(-time.Hour), NotAfter: now.Add(certValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, host)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return Certificate{}, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return Certificate{}, err
	}
	if err := writePEM(certPath, "CERTIFICATE", der, 0o644); err != nil {
		return Certificate{}, err
	}
	if err := writePEM(keyPath, "EC PRIVATE KEY", keyDER, 0o600); err != nil {
		return Certificate{}, err
	}
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return Certificate{}, err
	}
	return describe(pair, "self-signed")
}

func DefaultHosts() []string {
	hosts := []string{"localhost", "127.0.0.1", "::1"}
	if name, err := os.Hostname(); err == nil && name != "" {
		hosts = append(hosts, name)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, addr := range addrs {
			if ipnet, ok := addr.(*net.IPNet); ok && !ipnet.IP.IsUnspecified() && !ipnet.IP.IsLinkLocalMulticast() {
				hosts = append(hosts, ipnet.IP.String())
			}
		}
	}
	return hosts
}

func certUsable(leaf *x509.Certificate, hosts []string) bool {
	now := time.Now()
	if now.Before(leaf.NotBefore) || now.Add(certRenewWhen).After(leaf.NotAfter) {
		return false
	}
	have := map[string]bool{}
	for _, name := range leaf.DNSNames {
		have[strings.ToLower(name)] = true
	}
	for _, ip := range leaf.IPAddresses {
		have[ip.String()] = true
	}
	for _, host := range hosts {
		if ip := net.ParseIP(host); ip != nil {
			host = ip.String()
		}
		if !have[host] {
			return false
		}
	}
	return true
}

func normalizeHosts(hosts []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, host := range hosts {
		host = strings.ToLower(strings.TrimSpace(strings.Trim(host, "[]")))
		if host == "" {
			continue
		}
		if ip := net.ParseIP(host); ip != nil {
			host = ip.String()
		}
		if !seen[host] {
			seen[host] = true
			out = append(out, host)
		}
	}
	sort.Strings(out)
	return out
}

func writePEM(path, kind string, der []byte, mode os.FileMode) error {
	data := pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der})
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tls-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(mode); err != nil && !errors.Is(err, os.ErrPermission) {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, path)
}

func describe(pair tls.Certificate, source string) (Certificate, error) {
	if len(pair.Certificate) == 0 {
		return Certificate{}, errors.New("certificate file is empty")
	}
	leaf := pair.Leaf
	if leaf == nil {
		parsed, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return Certificate{}, fmt.Errorf("parse certificate: %w", err)
		}
		leaf = parsed
	}
	sum := sha256.Sum256(pair.Certificate[0])
	hexSum := strings.ToUpper(hex.EncodeToString(sum[:]))
	parts := make([]string, 0, len(hexSum)/2)
	for i := 0; i < len(hexSum); i += 2 {
		parts = append(parts, hexSum[i:i+2])
	}
	hosts := append([]string{}, leaf.DNSNames...)
	for _, ip := range leaf.IPAddresses {
		hosts = append(hosts, ip.String())
	}
	return Certificate{TLS: pair, Fingerprint: strings.Join(parts, ":"), Source: source, Hosts: hosts}, nil
}

func (c Certificate) ServerConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{c.TLS},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}
}
