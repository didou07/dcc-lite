package app

type RemoteSettings struct {
	Enabled  bool   `json:"enabled"`
	Listen   string `json:"listen"`
	TLSHosts string `json:"tls_hosts"`
	TLSEmail string `json:"tls_email"`
}

type RemoteStatus struct {
	RemoteSettings
	Running     bool     `json:"running"`
	Managed     bool     `json:"managed"`
	PasswordSet bool     `json:"password_set"`
	Port        string   `json:"port,omitempty"`
	URLs        []string `json:"urls,omitempty"`
	Fingerprint string   `json:"fingerprint,omitempty"`
	CertSource  string   `json:"certificate,omitempty"`
	Error       string   `json:"error,omitempty"`
}

type RemoteController interface {
	Status() RemoteStatus

	Apply(RemoteSettings) (status RemoteStatus, newPassword string, err error)
	SetPassword(password string) error
	GeneratePassword() (string, error)
}
