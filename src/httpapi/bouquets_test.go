package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/didou07/dcc-lite/src/enigma2"
)

func TestBouquetDir(t *testing.T) {
	ok := map[string]string{
		"":                      defaultEnigma2Dir,
		"   ":                   defaultEnigma2Dir,
		"/etc/enigma2":          "/etc/enigma2",
		"/etc/enigma2/":         "/etc/enigma2",
		" /etc/enigma2 ":        "/etc/enigma2",
		"/etc//enigma2/../e2":   "/etc/e2",
		"/media/hdd/../../etc/": "/etc",
	}
	for in, want := range ok {
		got, err := bouquetDir(in)
		if err != nil || got != want {
			t.Errorf("bouquetDir(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"etc/enigma2", "../etc", "/etc/e\nnigma2", "/etc/e\x00", "/" + strings.Repeat("a", 300)} {
		if got, err := bouquetDir(in); err == nil {
			t.Errorf("bouquetDir(%q) = %q, expected an error", in, got)
		}
	}
}

func TestWriteBouquetErrorSeparatesUserMistakesFromServerErrors(t *testing.T) {
	rec := httptest.NewRecorder()
	writeBouquetError(rec, badInput("bouquet name %q is not allowed", "x/y"))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "bad_request") || !strings.Contains(rec.Body.String(), "x/y") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestNameCacheExpiresAndIsBounded(t *testing.T) {
	var c nameCache
	if _, ok := c.get("a"); ok {
		t.Fatal("hit on an empty cache")
	}
	names := map[enigma2.ServiceKey]string{{SID: 1}: "One"}
	c.put("a", names)
	if got, ok := c.get("a"); !ok || got[enigma2.ServiceKey{SID: 1}] != "One" {
		t.Fatalf("get = %v, %v", got, ok)
	}
	c.entries["a"] = nameCacheEntry{names: names, at: time.Now().Add(-nameCacheTTL - time.Second)}
	if _, ok := c.get("a"); ok {
		t.Fatal("expired entry was served")
	}
	if _, still := c.entries["a"]; still {
		t.Fatal("expired entry was not dropped")
	}
	for i := 0; i < 100; i++ {
		c.put(string(rune('A'+i%26))+strings.Repeat("x", i), names)
	}
	if len(c.entries) > 18 {
		t.Fatalf("cache grew to %d entries", len(c.entries))
	}
}

func TestServiceCacheExpiresBoundsAndForgets(t *testing.T) {
	var c serviceCache
	svcs := []enigma2.Service{{Ref: "1:0:1:1:1:1:1:0:0:0:", Name: "One"}}
	c.put("k", svcs)
	if got, ok := c.get("k"); !ok || len(got) != 1 {
		t.Fatalf("get = %v, %v", got, ok)
	}
	c.entries["k"] = serviceCacheEntry{services: svcs, at: time.Now().Add(-nameCacheTTL - time.Second)}
	if _, ok := c.get("k"); ok {
		t.Fatal("expired entry was served")
	}
	for i := 0; i < 50; i++ {
		c.put(strings.Repeat("y", i+1), svcs)
	}
	if len(c.entries) > 10 {
		t.Fatalf("cache grew to %d entries", len(c.entries))
	}
	c.forget()
	if _, ok := c.get("y"); ok || len(c.entries) != 0 {
		t.Fatal("forget did not clear the cache")
	}
}

func TestMergeServicesReplacesByRefAndKeepsOrder(t *testing.T) {
	base := []enigma2.Service{{Ref: "a", Name: "A"}, {Ref: "b", Name: "B"}}
	got := mergeServices(base, []enigma2.Service{{Ref: "b", Name: "B2"}, {Ref: "c", Name: "C"}, {Ref: "c", Name: "C2"}})
	want := []string{"A", "B2", "C2"}
	if len(got) != len(want) {
		t.Fatalf("got %+v", got)
	}
	for i, n := range want {
		if got[i].Name != n {
			t.Errorf("[%d] = %q, want %q", i, got[i].Name, n)
		}
	}
	if len(mergeServices(nil, nil)) != 0 {
		t.Error("merging nothing should give nothing")
	}
	if got := mergeServices(nil, []enigma2.Service{{Ref: "x"}}); len(got) != 1 {
		t.Errorf("merge into empty base = %+v", got)
	}
}
