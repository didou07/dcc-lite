package enigma2

import (
	"reflect"
	"strings"
	"testing"
)

const sampleBouquet = "#NAME Favourites\n" +
	"#SERVICE 1:64:1:0:0:0:0:0:0:0:\n#DESCRIPTION --- News ---\n" +
	"#SERVICE 1:0:19:283D:3FB:1:C00000:0:0:0:\n#DESCRIPTION Channel One\n" +
	"#SERVICE 1:0:1:6DCA:44D:1:C00000:0:0:0:\n"

func TestBouquetParseFormatRoundTrip(t *testing.T) {
	b, err := ParseBouquet(sampleBouquet)
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "Favourites" || len(b.Entries) != 3 {
		t.Fatalf("parsed %+v", b)
	}
	if !b.Entries[0].IsMarker() || b.Entries[1].IsMarker() {
		t.Error("marker detection wrong")
	}
	if b.Entries[1].Description != "Channel One" || b.Entries[2].HasDescription {
		t.Error("descriptions attached to the wrong entries")
	}
	if got := b.Format(); got != sampleBouquet {
		t.Fatalf("format changed the file:\n%q\nvs\n%q", got, sampleBouquet)
	}
}

func TestBouquetHandlesCRLF(t *testing.T) {
	b, err := ParseBouquet(strings.ReplaceAll(sampleBouquet, "\n", "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	if b.Name != "Favourites" || b.Entries[1].Description != "Channel One" {
		t.Fatalf("CRLF not handled: %+v", b)
	}
	if strings.Contains(b.Format(), "\r") {
		t.Error("output should be normalised to LF")
	}
}

func TestParseBouquetRejectsGarbage(t *testing.T) {
	if _, err := ParseBouquet("hello\nworld\n"); err == nil {
		t.Fatal("non-bouquet text accepted")
	}
}

func TestParseBouquetEntryLimit(t *testing.T) {
	var sb strings.Builder
	sb.WriteString("#NAME big\n")
	for i := 0; i <= MaxEntries; i++ {
		sb.WriteString("#SERVICE 1:0:1:1:1:1:1:0:0:0:\n")
	}
	if _, err := ParseBouquet(sb.String()); err == nil {
		t.Fatal("over-limit bouquet accepted")
	}
}

func TestFormatCannotBeInjectedWithNewlines(t *testing.T) {
	b := Bouquet{Name: "a\n#SERVICE evil", Entries: []Entry{{Ref: "1:0:1:1:1:1:1:0:0:0:", Description: "x\n#SERVICE evil", HasDescription: true}}}
	out := b.Format()
	if strings.Count(out, "\n") != 3 {
		t.Fatalf("newline injection possible:\n%s", out)
	}
}

func TestNewMarkerPicksNextFreeNumber(t *testing.T) {
	b, _ := ParseBouquet(sampleBouquet)
	m := b.NewMarker("New\nTitle")
	if m.Ref != "1:64:2:0:0:0:0:0:0:0:" || !m.IsMarker() || strings.Contains(m.Description, "\n") {
		t.Fatalf("marker = %+v", m)
	}
	if got := (Bouquet{}).NewMarker("x").Ref; got != "1:64:1:0:0:0:0:0:0:0:" {
		t.Fatalf("first marker = %q", got)
	}
}

func TestValidateEntry(t *testing.T) {
	if err := ValidateEntry(Entry{Ref: "1:0:1:6DCA:44D:1:C00000:0:0:0:", Description: "ok"}); err != nil {
		t.Fatal(err)
	}
	for _, e := range []Entry{{Ref: ""}, {Ref: "a:b"}, {Ref: "1:0:1:1\n:2"}, {Ref: "1:0:1:1:", Description: "x\ny"}, {Ref: strings.Repeat("1:", 600)}} {
		if ValidateEntry(e) == nil {
			t.Errorf("%+v accepted", e)
		}
	}
}

func TestValidBouquetFileAndKind(t *testing.T) {
	for _, n := range []string{"userbouquet.favourites.tv", "bouquets.radio", "a.tv"} {
		if !ValidBouquetFile(n) {
			t.Errorf("%q rejected", n)
		}
	}
	for _, n := range []string{"", "../x.tv", "a/b.tv", "a..b.tv", ".hidden.tv", "x.txt", "x.tv;rm", "a b.tv"} {
		if ValidBouquetFile(n) {
			t.Errorf("%q accepted", n)
		}
	}
	if KindOf("a.radio") != "radio" || KindOf("a.tv") != "tv" {
		t.Error("KindOf wrong")
	}
}

func TestIndexAddRemoveMove(t *testing.T) {
	idx, err := ParseIndex(`#NAME Bouquets (TV)
#SERVICE 1:7:1:0:0:0:0:0:0:0:FROM BOUQUET "userbouquet.a.tv" ORDER BY bouquet
#SERVICE 1:7:1:0:0:0:0:0:0:0:FROM BOUQUET "userbouquet.b.tv" ORDER BY bouquet
`)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(idx.Files(), []string{"userbouquet.a.tv", "userbouquet.b.tv"}) {
		t.Fatalf("files = %v", idx.Files())
	}
	idx.Add("userbouquet.c.tv")
	idx.Add("userbouquet.r.radio")
	if !idx.Has("userbouquet.c.tv") || !strings.Contains(idx.Lines[3].Raw, "1:7:2:") {
		t.Fatalf("add wrong: %+v", idx.Lines)
	}
	if !idx.Move("userbouquet.c.tv", -2) || idx.Files()[0] != "userbouquet.c.tv" {
		t.Fatalf("move failed: %v", idx.Files())
	}
	if idx.Move("userbouquet.c.tv", -1) || idx.Move("missing.tv", 1) || idx.Move("userbouquet.a.tv", 0) {
		t.Error("invalid moves must report false")
	}
	if !idx.Remove("userbouquet.a.tv") || idx.Has("userbouquet.a.tv") || idx.Remove("userbouquet.a.tv") {
		t.Error("remove wrong")
	}
	round, err := ParseIndex(idx.Format())
	if err != nil || !reflect.DeepEqual(round.Files(), idx.Files()) {
		t.Fatalf("index round trip: %v %v", err, round.Files())
	}
}
