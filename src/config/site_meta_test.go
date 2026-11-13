package config

import "testing"

func TestSiteMetadataRoundTrip(t *testing.T) {
	in := Profile{
		ID: "abcdefgh1234", Name: "Living room", Host: "192.168.1.20", Username: "root", Password: "pw",
		Enabled: true, Group: "Home", Color: "teal", Favorite: true, StartDir: "/etc/enigma2", Note: "Vu+ Uno 4K",
	}
	if err := in.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	data := encodeINI(Document{Profiles: []Profile{in}})
	doc, err := decodeINI(data)
	if err != nil {
		t.Fatalf("decode: %v\n%s", err, data)
	}
	if len(doc.Profiles) != 1 {
		t.Fatalf("profiles = %d", len(doc.Profiles))
	}
	out := doc.Profiles[0]
	if out.Group != in.Group || out.Color != in.Color || !out.Favorite || out.StartDir != in.StartDir || out.Note != in.Note {
		t.Fatalf("round trip lost data: %+v", out)
	}
}

func TestSiteMetadataValidation(t *testing.T) {
	base := Profile{ID: "abcdefgh1234", Name: "x", Host: "10.0.0.1", Username: "root", Enabled: true}
	bad := []Profile{}
	p := base
	p.Color = "chartreuse"
	bad = append(bad, p)
	p = base
	p.StartDir = "etc/enigma2"
	bad = append(bad, p)
	p = base
	p.Group = "a\nb"
	bad = append(bad, p)
	for i, b := range bad {
		if b.Validate() == nil {
			t.Errorf("case %d should be rejected", i)
		}
	}
	if err := base.Validate(); err != nil {
		t.Fatalf("base should be valid: %v", err)
	}
}
