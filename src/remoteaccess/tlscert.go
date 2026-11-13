package remoteaccess

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"fmt"
	"strings"
)

type Certificate struct {
	TLS         tls.Certificate
	Leaf        *x509.Certificate
	Fingerprint string
	Source      string
	Hosts       []string
}

func (c Certificate) VerifyTrustedHost(host string) error {
	leaf := c.Leaf
	if leaf == nil {
		if len(c.TLS.Certificate) == 0 {
			return fmt.Errorf("certificate chain is empty")
		}
		var err error
		leaf, err = x509.ParseCertificate(c.TLS.Certificate[0])
		if err != nil {
			return fmt.Errorf("parse leaf certificate: %w", err)
		}
	}
	intermediates := x509.NewCertPool()
	for _, der := range c.TLS.Certificate[1:] {
		intermediate, err := x509.ParseCertificate(der)
		if err != nil {
			return fmt.Errorf("parse intermediate certificate: %w", err)
		}
		intermediates.AddCert(intermediate)
	}
	_, err := leaf.Verify(x509.VerifyOptions{
		DNSName:       strings.TrimSpace(strings.Trim(host, "[]")),
		Intermediates: intermediates,
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	})
	return err
}

func LoadProvided(certFile, keyFile string) (Certificate, error) {
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return Certificate{}, err
	}
	return describe(pair, "provided")
}

func describe(pair tls.Certificate, source string) (Certificate, error) {
	if len(pair.Certificate) == 0 {
		return Certificate{}, fmt.Errorf("certificate file is empty")
	}
	leaf := pair.Leaf
	if leaf == nil {
		parsed, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return Certificate{}, fmt.Errorf("parse certificate: %w", err)
		}
		leaf = parsed
	}
	pair.Leaf = leaf
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
	return Certificate{TLS: pair, Leaf: leaf, Fingerprint: strings.Join(parts, ":"), Source: source, Hosts: hosts}, nil
}

func (c Certificate) ServerConfig() *tls.Config {
	return &tls.Config{
		Certificates: []tls.Certificate{c.TLS},
		MinVersion:   tls.VersionTLS12,
		NextProtos:   []string{"http/1.1"},
	}
}
