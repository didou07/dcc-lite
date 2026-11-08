package enigma2

import (
	"strings"
	"testing"
)

const sampleBouquet = "#NAME Favourites (TV)\n" +
	"#SERVICE 1:0:19:2B66:3F3:1:C00000:0:0:0:\n" +
	"#SERVICE 1:64:1:0:0:0:0:0:0:0:\n#DESCRIPTION ----- Sports -----\n" +
	"#SERVICE 1:0:1:6DCA:44D:1:C00000:0:0:0:\n#DESCRIPTION Das Erste\n" +
	"#SERVICE 4097:0:1:0:0:0:0:0:0:0:http%3a//example.org/live.m3u8:My Stream\n#DESCRIPTION My Stream\n"

func TestBouquetRoundTripKeepsEverything(t *testing.T) {
	b, err := ParseBouquet(sampleBouquet)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "Favourites (TV)" || len(b.Entries) != 4 {
		t.Fatalf("parsed %q with %d entries", b.Name, len(b.Entries))
	}
	if !b.Entries[1].IsMarker() || b.Entries[0].IsMarker() || b.Entries[3].IsMarker() {
		t.Fatal("marker detection wrong")
	}
	if b.Entries[0].HasDescription || !b.Entries[2].HasDescription || b.Entries[2].Description != "Das Erste" {
		t.Fatalf("descriptions wrong: %#v", b.Entries)
	}
	if got := b.Format(); got != sampleBouquet {
		t.Fatalf("round trip differs:\n%s\n---\n%s", got, sampleBouquet)
	}
}

func TestBouquetToleratesCRLFHeaderLinesAndBOM(t *testing.T) {
	text := "\ufeff#NAME Test\r\n#SORT 3\r\n#SERVICE 1:0:1:1:1:1:1:0:0:0:\r\n#DESCRIPTION A\r\n"
	b, err := ParseBouquet(text)
	if err != nil || b.Name != "Test" || len(b.Header) != 1 || b.Header[0] != "#SORT 3" || len(b.Entries) != 1 {
		t.Fatalf("parse = %#v, %v", b, err)
	}
	if got := b.Format(); got != "#NAME Test\n#SORT 3\n#SERVICE 1:0:1:1:1:1:1:0:0:0:\n#DESCRIPTION A\n" {
		t.Fatalf("format = %q", got)
	}
}

func TestParseBouquetRejectsNonBouquets(t *testing.T) {
	if _, err := ParseBouquet("hello world\nnothing here\n"); err == nil {
		t.Fatal("garbage accepted")
	}
	if _, err := ParseBouquet(""); err == nil {
		t.Fatal("empty file accepted")
	}
}

func TestFormatSanitisesInjectedNewlines(t *testing.T) {
	b := Bouquet{Name: "A\n#SERVICE 1:0:1:1:1:1:1:0:0:0:", Entries: []Entry{{Ref: "1:0:1:1:1:1:1:0:0:0:", Description: "x\r\n#SERVICE evil", HasDescription: true}}}
	out := b.Format()
	if strings.Count(out, "\n") != 3 {
		t.Fatalf("a newline survived sanitising:\n%s", out)
	}
}

func TestNewMarkerNumbersAreUnique(t *testing.T) {
	b, _ := ParseBouquet(sampleBouquet)
	m := b.NewMarker("News\n")
	if m.Ref != "1:64:2:0:0:0:0:0:0:0:" || m.Description != "News " || !m.IsMarker() {
		t.Fatalf("marker = %#v", m)
	}
	b.Entries = append(b.Entries, m)
	if next := b.NewMarker("x"); next.Ref != "1:64:3:0:0:0:0:0:0:0:" {
		t.Fatalf("second marker = %v", next.Ref)
	}
}

func TestValidateEntry(t *testing.T) {
	good := Entry{Ref: "1:0:19:2B66:3F3:1:C00000:0:0:0:", Description: "ok"}
	if err := ValidateEntry(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []Entry{{Ref: ""}, {Ref: "nonsense"}, {Ref: "1:0:1:1:1:1:1\n#NAME x"}, {Ref: good.Ref, Description: "a\nb"}, {Ref: strings.Repeat("1:", 600)}} {
		if err := ValidateEntry(bad); err == nil {
			t.Errorf("accepted %#v", bad)
		}
	}
}

func TestBouquetFileNamesCannotEscapeDirectory(t *testing.T) {
	for _, ok := range []string{"userbouquet.favourites.tv", "userbouquet.dbe01.radio", "userbouquet.my-list_2.tv"} {
		if !ValidBouquetFile(ok) {
			t.Errorf("%q rejected", ok)
		}
	}
	for _, bad := range []string{"", "../etc/passwd", "a/b.tv", "..tv", "x..y.tv", ".hidden.tv", "userbouquet.x.txt", "a b.tv", "bouquets.tv\n", "/etc/enigma2/x.tv", `a\b.tv`} {
		if ValidBouquetFile(bad) {
			t.Errorf("%q accepted", bad)
		}
	}
}

const sampleIndex = "#NAME Bouquets (TV)\n" +
	"#SERVICE 1:7:1:0:0:0:0:0:0:0:FROM BOUQUET \"userbouquet.favourites.tv\" ORDER BY bouquet\n" +
	"#SERVICE 1:7:1:0:0:0:0:0:0:0:FROM BOUQUET \"userbouquet.dbe00.tv\" ORDER BY bouquet\n" +
	"#SERVICE 1:134:1:0:0:0:0:0:0:0:FROM BOUQUET \"alternatives.x.tv\" ORDER BY bouquet\n"

func TestIndexParseEditAndFormat(t *testing.T) {
	idx, err := ParseIndex(sampleIndex)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(idx.Files(), ","); got != "userbouquet.favourites.tv,userbouquet.dbe00.tv,alternatives.x.tv" {
		t.Fatalf("files = %s", got)
	}
	if idx.Format() != sampleIndex {
		t.Fatal("index round trip differs")
	}
	if !idx.Move("userbouquet.dbe00.tv", -1) || strings.Join(idx.Files(), ",")[:21] != "userbouquet.dbe00.tv," {
		t.Fatalf("move up failed: %v", idx.Files())
	}
	if idx.Move("userbouquet.dbe00.tv", -1) {
		t.Fatal("moved above the first line")
	}
	idx.Add("userbouquet.dccabc123.tv")
	idx.Add("userbouquet.dccabc456.radio")
	if !strings.Contains(idx.Format(), `1:7:1:0:0:0:0:0:0:0:FROM BOUQUET "userbouquet.dccabc123.tv" ORDER BY bouquet`) ||
		!strings.Contains(idx.Format(), `1:7:2:0:0:0:0:0:0:0:FROM BOUQUET "userbouquet.dccabc456.radio" ORDER BY bouquet`) {
		t.Fatalf("added lines wrong:\n%s", idx.Format())
	}
	if !idx.Remove("userbouquet.favourites.tv") || idx.Has("userbouquet.favourites.tv") || idx.Remove("nothing.tv") {
		t.Fatal("remove behaved wrongly")
	}
	if _, err := ParseIndex("random"); err == nil {
		t.Fatal("garbage accepted as index")
	}
}

func TestKeyOfRef(t *testing.T) {
	key, ok := KeyOfRef("1:0:19:2B66:3F3:1:C00000:0:0:0:")
	if !ok || key != (ServiceKey{SID: 0x2B66, TSID: 0x3F3, ONID: 1, Namespace: 0xC00000}) {
		t.Fatalf("key = %#v ok=%v", key, ok)
	}
	for _, ref := range []string{"1:64:1:0:0:0:0:0:0:0:", "4097:0:1:0:0:0:0:0:0:0:http%3a//x:N", "garbage", "1:0:1:ZZ:1:1:1:0:0:0:"} {
		if _, ok := KeyOfRef(ref); ok {
			t.Errorf("%q must not resolve to a service key", ref)
		}
	}
}

const sampleLamedb4 = `eDVB services /4/
transponders
00c00000:0001:0001
	s 11494000:27500000:1:5:192:0:0
/
end
services
2b66:00c00000:03f3:0001:25:0
Das Erste HD
p:ARD,c:00001b58,c:01001b59
6dca:00c00000:044d:0001:1:0
ZDF
p:ZDF,c:00006dca
end
Have a nice day;)
`

func TestParseLamedbV4(t *testing.T) {
	names := ParseLamedb(sampleLamedb4)
	if len(names) != 2 {
		t.Fatalf("got %d names: %v", len(names), names)
	}
	k, _ := KeyOfRef("1:0:19:2B66:3F3:1:C00000:0:0:0:")
	if names[k] != "Das Erste HD" {
		t.Fatalf("name = %q", names[k])
	}
	k2, _ := KeyOfRef("1:0:1:6DCA:44D:1:C00000:0:0:0:")
	if names[k2] != "ZDF" {
		t.Fatalf("name = %q", names[k2])
	}
}

func TestParseLamedbV5(t *testing.T) {
	text := "eDVB services /5/\n# Transponders: 1\nt:00c00000:0001:0001,s:11494000:27500000:1:5:192:0:0\n# Services: 1\ns:2b66:00c00000:03f3:0001:25:0,\"Das Erste HD\",p:ARD,c:00001b58\n"
	names := ParseLamedb(text)
	k, _ := KeyOfRef("1:0:19:2B66:3F3:1:C00000:0:0:0:")
	if names[k] != "Das Erste HD" {
		t.Fatalf("v5 names = %v", names)
	}
}

func TestDisplayName(t *testing.T) {
	names := map[ServiceKey]string{{SID: 0x2B66, TSID: 0x3F3, ONID: 1, Namespace: 0xC00000}: "Das Erste HD"}
	if got := DisplayName(Entry{Ref: "1:0:19:2B66:3F3:1:C00000:0:0:0:"}, names); got != "Das Erste HD" {
		t.Fatalf("service = %q", got)
	}
	if got := DisplayName(Entry{Ref: "1:64:1:0:0:0:0:0:0:0:", Description: "Sports"}, names); got != "Sports" {
		t.Fatalf("marker = %q", got)
	}
	if got := DisplayName(Entry{Ref: "1:0:1:9:9:9:9:0:0:0:"}, names); got != "" {
		t.Fatalf("unknown = %q", got)
	}
	if got := DisplayName(Entry{Ref: "4097:0:1:0:0:0:0:0:0:0:u:N", Description: "My Stream"}, names); got != "My Stream" {
		t.Fatalf("stream = %q", got)
	}
}

func TestParseServicesBuildsReferencesAndSeparatesRadio(t *testing.T) {
	v4 := "eDVB services /4/\ntransponders\nend\nservices\n" +
		"2b66:00c00000:03f3:0001:25:0\nDas Erste HD\np:ARD,c:00\n" +
		"6dca:00c00000:044d:0001:1:0\nZDF\np:ZDF\n" +
		"1234:00c00000:0001:0001:2:0\nRadio One\np:Ex\nend\n" +
		"6dca:00c00000:044d:0001:1:0\nZDF again\np:ZDF\nend\n"
	got := ParseServices(v4)
	if len(got) != 3 {
		t.Fatalf("services = %+v", got)
	}
	if got[0].Ref != "1:0:19:2B66:3F3:1:C00000:0:0:0:" || got[0].Provider != "ARD" || got[0].Radio {
		t.Fatalf("first = %+v", got[0])
	}
	if !got[2].Radio || got[2].Name != "Radio One" {
		t.Fatalf("radio = %+v", got[2])
	}
	v5 := "eDVB services /5/\n" + `s:2b66,00c00000,03f3,0001,25,0,"Das Erste HD",p:ARD` + "\n"
	if s := ParseServices(v5); len(s) != 1 || s[0].Ref != "1:0:19:2B66:3F3:1:C00000:0:0:0:" || s[0].Name != "Das Erste HD" {
		t.Fatalf("v5 = %+v", s)
	}
}

// Enigma2 (eDVBDB::loadBouquet) accepts "#SERVICE:" and "#DESCRIPTION:" as well.
func TestParseBouquetAcceptsColonDirectives(t *testing.T) {
	text := "#NAME Colon\r\n#SERVICE:1:0:1:6DCA:44D:1:C00000:0:0:0:\r\n#DESCRIPTION: ZDF\r\n#SERVICE:1:64:1:0:0:0:0:0:0:0:\r\n#DESCRIPTION:News\r\n"
	b, err := ParseBouquet(text)
	if err != nil || len(b.Entries) != 2 {
		t.Fatalf("parse = %#v, %v", b, err)
	}
	if b.Entries[0].Ref != "1:0:1:6DCA:44D:1:C00000:0:0:0:" || b.Entries[0].Description != "ZDF" || b.Entries[1].Description != "News" {
		t.Fatalf("entries = %#v", b.Entries)
	}
	idx, err := ParseIndex("#NAME Bouquets (TV)\n#SERVICE:1:7:1:0:0:0:0:0:0:0:FROM BOUQUET \"userbouquet.a.tv\" ORDER BY bouquet\n")
	if err != nil || len(idx.Files()) != 1 || idx.Files()[0] != "userbouquet.a.tv" {
		t.Fatalf("index = %#v, %v", idx, err)
	}
	if got := idx.Format(); !strings.Contains(got, "#SERVICE 1:7:1:0:0:0:0:0:0:0:FROM BOUQUET") {
		t.Fatalf("index is not written back in the canonical form: %q", got)
	}
}

// The third field of a reference is hexadecimal in Enigma2.
func TestNewMarkerNumbersAreHexadecimal(t *testing.T) {
	b := Bouquet{Entries: []Entry{{Ref: "1:64:9:0:0:0:0:0:0:0:"}, {Ref: "1:64:A:0:0:0:0:0:0:0:"}}}
	if m := b.NewMarker("x"); m.Ref != "1:64:B:0:0:0:0:0:0:0:" {
		t.Fatalf("after A: %s", m.Ref)
	}
	b.Entries = append(b.Entries, Entry{Ref: "1:64:F:0:0:0:0:0:0:0:"})
	if m := b.NewMarker("x"); m.Ref != "1:64:10:0:0:0:0:0:0:0:" {
		t.Fatalf("after F: %s", m.Ref)
	}
}

func TestCleanNameAndSplitDescription(t *testing.T) {
	if got := CleanName("\u0086Das Erste\u0087 HD\u008a "); got != "Das Erste HD" {
		t.Fatalf("CleanName = %q", got)
	}
	if n, p := SplitDescription("ZDF•ZDFvision"); n != "ZDF" || p != "ZDFvision" {
		t.Fatalf("split = %q, %q", n, p)
	}
	if n, p := SplitDescription("Plain"); n != "Plain" || p != "" {
		t.Fatalf("split = %q, %q", n, p)
	}
	names := ParseLamedb("eDVB services /5/\ns:2b66:00c00000:03f3:0001:25:0,\"\u0086Das Erste HD\u0087\",p:ARD\n")
	k, _ := KeyOfRef("1:0:19:2B66:3F3:1:C00000:0:0:0:")
	if names[k] != "Das Erste HD" {
		t.Fatalf("name = %q", names[k])
	}
}

// A name given in the bouquet wins over the lamedb name, as it does on the receiver.
func TestDisplayNamePrefersBouquetNameWithoutProvider(t *testing.T) {
	names := map[ServiceKey]string{{SID: 0x2B66, TSID: 0x3F3, ONID: 1, Namespace: 0xC00000}: "Das Erste HD"}
	e := Entry{Ref: "1:0:19:2B66:3F3:1:C00000:0:0:0:", Description: "My Erste•ARD", HasDescription: true}
	if got := DisplayName(e, names); got != "My Erste" {
		t.Fatalf("display = %q", got)
	}
}

func TestIsTVType(t *testing.T) {
	for _, tv := range []int{1, 17, 22, 25, 31, 32, 134, 195} {
		if !IsTVType(tv) {
			t.Errorf("type %d should be TV", tv)
		}
	}
	for _, other := range []int{2, 3, 10, 12, 16, 128} {
		if IsTVType(other) {
			t.Errorf("type %d should not be TV", other)
		}
	}
}
