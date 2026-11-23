package config

import (
	"reflect"
	"strings"
	"testing"
)

func sampleProfile() Profile {
	return Profile{
		ID: "abcd1234efgh", Name: "Living room", Host: "192.168.1.10",
		Username: "root", Password: ` pa"ss\word `, FTPPort: 2121, TelnetPort: 2323,
		Enabled: true, Group: "Home", Color: "blue", Favorite: true,
		StartDir: "/etc/enigma2", Note: "note",
	}
}

func TestINIRoundTrip(t *testing.T) {
	doc := Document{
		Profiles: []Profile{sampleProfile()},
		Remote:   Remote{Enabled: true, Listen: "127.0.0.1:8443", PasswordSalt: "s", PasswordHash: "h", PasswordIterations: 1000},
	}
	got, err := decodeINI(encodeINI(doc))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !reflect.DeepEqual(got, doc) {
		t.Fatalf("round trip mismatch:\n got %#v\nwant %#v", got, doc)
	}
}

func TestINIPasswordWithSpecialCharsSurvives(t *testing.T) {
	for _, pw := range []string{"", "plain", " leading", "trailing ", `quo"te`, "semi;colon", "#hash", "="} {
		p := sampleProfile()
		p.Password = pw
		got, err := decodeINI(encodeINI(Document{Profiles: []Profile{p}, Remote: Remote{Listen: "x"}}))
		if err != nil {
			t.Fatalf("password %q: %v", pw, err)
		}
		if got.Profiles[0].Password != pw {
			t.Fatalf("password %q came back as %q", pw, got.Profiles[0].Password)
		}
	}
}

func TestINIDecodeRejectsBadInput(t *testing.T) {
	good := string(encodeINI(Document{Profiles: []Profile{sampleProfile()}, Remote: Remote{Listen: "x"}}))
	cases := map[string]string{
		"missing meta":      strings.Replace(good, "[Meta]", "[Other]", 1),
		"wrong format":      strings.Replace(good, "Format=2", "Format=9", 1),
		"duplicate key":     strings.Replace(good, "Name=Living room", "Name=a\nName=b", 1),
		"unknown key":       strings.Replace(good, "Color=blue", "Bogus=1", 1),
		"bad bool":          strings.Replace(good, "Favorite=yes", "Favorite=maybe", 1),
		"malformed quote":   strings.Replace(good, "Note=note", `Note="oops`, 1),
		"duplicate section": good + "[Device1]\n",
		"partial password":  strings.Replace(good, "[Device1]", "PasswordSalt=s\n[Device1]", 1),
	}
	for name, in := range cases {
		if _, err := decodeINI([]byte(in)); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestProfileValidate(t *testing.T) {
	if err := sampleProfile().Validate(); err != nil {
		t.Fatalf("valid profile rejected: %v", err)
	}
	mutations := map[string]func(*Profile){
		"empty name":         func(p *Profile) { p.Name = "" },
		"host with port":     func(p *Profile) { p.Host = "1.2.3.4:80" },
		"host with path":     func(p *Profile) { p.Host = "a/b" },
		"bad port":           func(p *Profile) { p.FTPPort = 70000 },
		"bad scheme":         func(p *Profile) { p.Scheme = "ftp" },
		"insecure over http": func(p *Profile) { p.InsecureTLS = true },
		"relative startdir":  func(p *Profile) { p.StartDir = "etc" },
		"bad mac":            func(p *Profile) { p.MAC = "nope" },
		"newline password":   func(p *Profile) { p.Password = "a\nb" },
		"bad id":             func(p *Profile) { p.ID = "short" },
	}
	for name, mutate := range mutations {
		p := sampleProfile()
		mutate(&p)
		if p.Validate() == nil {
			t.Errorf("%s: expected validation error", name)
		}
	}
}

func TestEffectivePorts(t *testing.T) {
	p := Profile{}
	if p.EffectiveFTPPort() != DefaultFTPPort || p.EffectiveTelnetPort() != DefaultTelnetPort {
		t.Fatal("defaults not applied")
	}
	p.FTPPort, p.TelnetPort = 2121, 2323
	if p.EffectiveFTPPort() != 2121 || p.EffectiveTelnetPort() != 2323 {
		t.Fatal("explicit ports ignored")
	}
}
