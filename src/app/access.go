package app

// RemoteSettings are the options of the optional password-protected HTTPS
// listener that can be changed from the local web interface.
type RemoteSettings struct {
	Enabled  bool   `json:"enabled"`
	Listen   string `json:"listen"`    // for example 0.0.0.0:8788
	TLSHosts string `json:"tls_hosts"` // public IP / dynamic DNS names: put in the self-signed certificate and shown in the links
	// PlainHTTP serves the listener without TLS. Everything, the password included,
	// then travels unencrypted. Off by default.
	PlainHTTP bool `json:"plain_http"`
}

// RemoteStatus describes the HTTPS listener as it is right now.
type RemoteStatus struct {
	RemoteSettings
	Running     bool     `json:"running"`
	Managed     bool     `json:"managed"` // false when --remote-listen on the command line controls it
	PasswordSet bool     `json:"password_set"`
	Port        string   `json:"port,omitempty"`
	URLs        []string `json:"urls,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	CertSource  string   `json:"certificate,omitempty"`
	Error       string   `json:"error,omitempty"`
}

// RemoteController starts, stops and configures the HTTPS listener. It is
// implemented by the executable, which owns the listener; it is nil in tests.
type RemoteController interface {
	Status() RemoteStatus
	// Apply stores the settings and (re)starts or stops the listener. It returns
	// a password only when one had to be generated because none existed yet.
	Apply(RemoteSettings) (status RemoteStatus, newPassword string, err error)
	SetPassword(password string) error
	GeneratePassword() (string, error)
}
