package config

const DefaultRemoteListen = "0.0.0.0:8788"

type Remote struct {
	Enabled            bool
	Listen             string
	TLSHosts           string
	PlainHTTP          bool
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
