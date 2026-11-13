package config

const DefaultRemoteListen = "0.0.0.0:443"

type Remote struct {
	Enabled            bool
	Listen             string
	TLSHosts           string
	TLSEmail           string
	PasswordSalt       string
	PasswordHash       string
	PasswordIterations int
}

func (r Remote) EffectiveListen() string {
	if r.Listen == "" {
		return DefaultRemoteListen
	}
	return r.Listen
}
