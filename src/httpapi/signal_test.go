package httpapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

func TestBuildLiveSignal(t *testing.T) {
	got := buildLiveSignal(map[string]any{"snr": "78 %", "snr_db": "12.50 dB", "agc": float64(91), "ber": "2", "tunertype": "DVB-S2", "tunernumber": float64(1), "inStandby": "false"})
	if !got.Available || !got.Locked || got.SNR != 78 || !got.HasDB || got.SNRdB != 12.5 || got.AGC != 91 || got.BER != 2 || got.Tuner != "DVB-S2 #1" {
		t.Fatalf("signal = %+v", got)
	}
	// Older images spell AGC "acg".
	if got := buildLiveSignal(map[string]any{"snr": float64(40), "acg": float64(55)}); got.AGC != 55 || got.HasDB {
		t.Fatalf("acg signal = %+v", got)
	}
	// Standby and empty answers are not a reading.
	if got := buildLiveSignal(map[string]any{"snr": "", "agc": ""}); got.Available {
		t.Fatalf("empty = %+v", got)
	}
	if got := buildLiveSignal(map[string]any{"snr": "50", "inStandby": "true"}); got.Locked || !got.Standby {
		t.Fatalf("standby = %+v", got)
	}
	// SNR 0 means no lock (the dish points at nothing).
	if got := buildLiveSignal(map[string]any{"snr": "0", "agc": "30"}); !got.Available || got.Locked {
		t.Fatalf("no lock = %+v", got)
	}
}

func TestDeviceSignalEndpoint(t *testing.T) {
	host, port, _ := fakeReceiver(t, func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/tunersignal":
			_, _ = w.Write([]byte(`{"snr":"66","snr_db":"9.80","agc":70,"ber":0,"tunertype":"DVB-S2","tunernumber":0}`))
		case "/api/getcurrent":
			_, _ = w.Write([]byte(`{"info":{"name":"Das Erste HD","ref":"1:0:19:1:1:1:1:0:0:0:"}}`))
		default:
			http.NotFound(w, r)
		}
	})
	h := testServer(t)
	id := createDevice(t, h, host, port)
	res := request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/signal?channel=1", nil)
	var got liveSignal
	if err := json.Unmarshal(res.Body.Bytes(), &got); err != nil || res.Code != http.StatusOK {
		t.Fatalf("signal = %d %s", res.Code, res.Body.String())
	}
	if !got.Available || got.SNR != 66 || got.SNRdB != 9.8 || got.Channel != "Das Erste HD" {
		t.Fatalf("signal = %+v", got)
	}
	// Without channel=1 the channel call is not made.
	res = request(t, h, http.MethodGet, "/api/v1/profiles/"+id+"/signal", nil)
	_ = json.Unmarshal(res.Body.Bytes(), &got)
	if res.Code != http.StatusOK {
		t.Fatalf("plain signal = %d %s", res.Code, res.Body.String())
	}
	var plain liveSignal
	_ = json.Unmarshal(res.Body.Bytes(), &plain)
	if plain.Channel != "" {
		t.Fatalf("channel read without asking: %+v", plain)
	}
	if res := request(t, h, http.MethodGet, "/api/v1/profiles/missing/signal", nil); res.Code != http.StatusNotFound {
		t.Fatalf("missing device = %d", res.Code)
	}
}
