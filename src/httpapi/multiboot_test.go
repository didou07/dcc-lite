package httpapi

import (
	"net/http"
	"strings"
	"testing"
)

// The shape below is what OpenWebif's controllers/models/control.py getMultiBootSlots builds.
const multibootReply = `{"result":true,"multiboot":true,"currentSlot":"3","currentBootCode":"","slots":[
 {"slot":"1","label":"1","bootCode":"","type":"eMMC","device":"/dev/mmcblk0p4","imagename":"OpenPLi","status":"active","ubi":false,"current":false,"description":""},
 {"slot":"3","label":"3","bootCode":"","imagename":"OpenATV 8.0.2","current":true},
 {"slot":"4","label":"4 (kernel 1)","bootCode":"1","imagename":"EGAMI","current":false},
 {"slot":"4","label":"4 (kernel 2)","bootCode":"12","imagename":"EGAMI","current":false},
 {"slot":"R","label":"R","bootCode":"","imagename":"Unknown","current":false},
 {"slot":"bad/slot","label":"x","bootCode":"","imagename":"x"}]}`

func TestParseBootSlots(t *testing.T) {
	slots := parseBootSlots([]byte(multibootReply))
	if len(slots) != 5 {
		t.Fatalf("got %d slots: %+v", len(slots), slots)
	}
	if slots[0].Label != "1:OpenPLi" || slots[0].Slot != "1" || slots[0].Code != "" || slots[0].Current {
		t.Fatalf("first slot %+v", slots[0])
	}
	if !slots[1].Current || slots[1].Label != "3:OpenATV 8.0.2" {
		t.Fatalf("current slot %+v", slots[1])
	}
	if slots[2].Code != "1" || slots[3].Code != "12" || slots[2].Label != "4 (kernel 1):EGAMI" {
		t.Fatalf("boot codes %+v %+v", slots[2], slots[3])
	}
	if slots[4].Label != "R" {
		t.Fatalf("an unknown image name must not be shown: %+v", slots[4])
	}
}

func TestParseBootSlotsRejectsNonsense(t *testing.T) {
	for _, body := range []string{``, `not json`, `{"result":true}`, `{"multiboot":false,"slots":[{"slot":"1"}]}`, `{"multiboot":true,"slots":[]}`, `{"multiboot":true,"slots":[{"slot":"a/b"}]}`} {
		if slots := parseBootSlots([]byte(body)); len(slots) != 0 {
			t.Fatalf("%q gave %+v", body, slots)
		}
	}
}

func TestDeviceMultibootListsAndSwitches(t *testing.T) {
	host, port, seen := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/multiboot/get":
			_, _ = w.Write([]byte(multibootReply))
		case "/multiboot/set":
			_, _ = w.Write([]byte(`{"result":true,"statetext":"Rebooting to slot 4","id":""}`))
		default:
			http.NotFound(w, r)
		}
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)

	rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/multiboot", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"supported":true`) || !strings.Contains(rec.Body.String(), `"code":"12"`) {
		t.Fatalf("list = %d %s", rec.Code, rec.Body)
	}

	rec = request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/multiboot", []byte(`{"slot":"4","code":"12"}`))
	if rec.Code != http.StatusOK || (*seen)[len(*seen)-1] != "/multiboot/set?bootcode=12&slot=4" {
		t.Fatalf("switch = %d, receiver asked %v", rec.Code, *seen)
	}

	n := len(*seen)
	// The running slot, an unlisted pair and malformed values never reach the set call.
	for body, want := range map[string]int{
		`{"slot":"3","code":""}`:     http.StatusConflict,
		`{"slot":"9","code":""}`:     http.StatusUnprocessableEntity,
		`{"slot":"4","code":"7"}`:    http.StatusUnprocessableEntity,
		`{"slot":"1&x=1","code":""}`: http.StatusBadRequest,
		`{"slot":"4","code":"a&b"}`:  http.StatusBadRequest,
		`{}`:                         http.StatusBadRequest,
	} {
		if rec := request(t, h, http.MethodPost, "/api/v1/profiles/"+id+"/multiboot", []byte(body)); rec.Code != want {
			t.Fatalf("%s = %d, want %d", body, rec.Code, want)
		}
	}
	for _, path := range (*seen)[n:] {
		if strings.HasPrefix(path, "/multiboot/set") {
			t.Fatalf("a rejected slot reached the receiver: %v", (*seen)[n:])
		}
	}
}

func TestDeviceMultibootWithoutSupport(t *testing.T) {
	for name, handler := range map[string]http.HandlerFunc{
		"image without the controller": func(w http.ResponseWriter, r *http.Request) { http.NotFound(w, r) },
		"no MultiBoot hardware": func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"result":true,"multiboot":false,"slots":[]}`))
		},
	} {
		host, port, _ := fakeReceiver(t, handler)
		h := testServer(t)
		id := createDevice(t, h, host, port)
		rec := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/multiboot", nil)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"supported":false`) || !strings.Contains(rec.Body.String(), `"slots":[]`) {
			t.Fatalf("%s = %d %s", name, rec.Code, rec.Body)
		}
	}
}
