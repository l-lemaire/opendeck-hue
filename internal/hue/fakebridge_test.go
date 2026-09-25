package hue

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeBridge is an HTTPS server that behaves like a Hue bridge for the
// endpoints this package uses. Its certificate mimics a real one: CN is the
// bridge id in upper case, issuer "root-bridge", no SAN.
type fakeBridge struct {
	*httptest.Server
	id          string
	fingerprint string
	// refusals is how many pairing attempts return "link button not
	// pressed" before one succeeds. atomic because the server handles
	// requests on other goroutines.
	refusals atomic.Int32
	appKey   string

	// Mutable v2 resources, keyed by id. mu guards them because the HTTP
	// server handles each request on its own goroutine.
	mu      sync.Mutex
	lights  map[string]map[string]any
	grouped map[string]map[string]any
	rooms   []map[string]any
	zones   []map[string]any
	puts    []string // "type/id" of every PUT, in order, for assertions
}

// Fixed ids so tests can refer to them.
const (
	lightKitchen  = "11111111-1111-1111-1111-111111111111"
	lightDesk     = "22222222-2222-2222-2222-222222222222"
	lightPlug     = "33333333-3333-3333-3333-333333333333"
	groupedKitchn = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	groupedOffice = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	roomKitchen   = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	zoneOffice    = "dddddddd-dddd-dddd-dddd-dddddddddddd"
)

func (fb *fakeBridge) seedResources() {
	fb.lights = map[string]map[string]any{
		lightKitchen: {"id": lightKitchen, "id_v1": "/lights/1", "type": "light",
			"metadata": map[string]any{"name": "Kitchen", "archetype": "sultan_bulb"},
			"on":       map[string]any{"on": true}, "dimming": map[string]any{"brightness": 80.0}},
		lightDesk: {"id": lightDesk, "id_v1": "/lights/2", "type": "light",
			"metadata": map[string]any{"name": "Desk lamp", "archetype": "spot_bulb"},
			"on":       map[string]any{"on": false}, "dimming": map[string]any{"brightness": 50.0}},
		lightPlug: {"id": lightPlug, "id_v1": "/lights/3", "type": "light",
			"metadata": map[string]any{"name": "Desk plug", "archetype": "plug"},
			"on":       map[string]any{"on": false}},
	}
	fb.grouped = map[string]map[string]any{
		groupedKitchn: {"id": groupedKitchn, "type": "grouped_light", "owner": map[string]any{"rid": roomKitchen, "rtype": "room"},
			"on": map[string]any{"on": true}, "dimming": map[string]any{"brightness": 80.0}},
		groupedOffice: {"id": groupedOffice, "type": "grouped_light", "owner": map[string]any{"rid": zoneOffice, "rtype": "zone"},
			"on": map[string]any{"on": false}, "dimming": map[string]any{"brightness": 50.0}},
	}
	fb.rooms = []map[string]any{{
		"id": roomKitchen, "type": "room", "metadata": map[string]any{"name": "Kitchen"},
		"children": []map[string]any{{"rid": "dev-1", "rtype": "device"}},
		"services": []map[string]any{{"rid": groupedKitchn, "rtype": "grouped_light"}},
	}}
	fb.zones = []map[string]any{{
		"id": zoneOffice, "type": "zone", "metadata": map[string]any{"name": "Office"},
		"children": []map[string]any{{"rid": lightDesk, "rtype": "light"}, {"rid": lightPlug, "rtype": "light"}},
		"services": []map[string]any{{"rid": groupedOffice, "rtype": "grouped_light"}},
	}}
}

// v2Handler serves /clip/v2/resource/{type} and {type}/{id} for the seeded
// resources, requiring the application key like the real bridge.
func (fb *fakeBridge) v2Handler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(appKeyHeader) != fb.appKey {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"errors":[{"description":"unauthorized user"}],"data":[]}`))
		return
	}
	fb.mu.Lock()
	defer fb.mu.Unlock()

	rtype, id := r.PathValue("type"), r.PathValue("id")
	write := func(status int, data any, errs ...string) {
		w.WriteHeader(status)
		env := map[string]any{"errors": []map[string]string{}, "data": data}
		for _, e := range errs {
			env["errors"] = append(env["errors"].([]map[string]string), map[string]string{"description": e})
		}
		json.NewEncoder(w).Encode(env)
	}
	var byID map[string]map[string]any
	var list []map[string]any
	switch rtype {
	case "light":
		byID = fb.lights
	case "grouped_light":
		byID = fb.grouped
	case "room":
		list = fb.rooms
	case "zone":
		list = fb.zones
	default:
		write(http.StatusNotFound, []any{}, "Not Found")
		return
	}
	if byID != nil {
		for _, v := range byID {
			list = append(list, v)
		}
	}

	switch {
	case r.Method == http.MethodGet && id == "":
		write(http.StatusOK, list)
	case r.Method == http.MethodGet:
		if res, ok := byID[id]; ok {
			write(http.StatusOK, []any{res})
		} else {
			write(http.StatusNotFound, []any{}, "Not Found")
		}
	case r.Method == http.MethodPut && byID != nil:
		res, ok := byID[id]
		if !ok {
			write(http.StatusNotFound, []any{}, "Not Found")
			return
		}
		var patch map[string]any
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			write(http.StatusBadRequest, []any{}, "invalid json")
			return
		}
		if _, hasDimming := patch["dimming"]; hasDimming && res["dimming"] == nil {
			write(http.StatusOK, []any{}, "invalid property: dimming not supported")
			return
		}
		for k, v := range patch { // shallow merge, like the bridge
			res[k] = v
		}
		fb.puts = append(fb.puts, rtype+"/"+id)
		write(http.StatusOK, []map[string]string{{"rid": id, "rtype": rtype}})
	default:
		write(http.StatusMethodNotAllowed, []any{}, "method not allowed")
	}
}

func newFakeBridge(t *testing.T, id string) *fakeBridge {
	t.Helper()
	fb := &fakeBridge{id: strings.ToLower(id), appKey: "fake-app-key-0123456789"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/0/config", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"name": "Fake Bridge", "bridgeid": strings.ToUpper(fb.id), "modelid": "BSB002",
			"apiversion": "1.78.0", "swversion": "1978293000",
		})
	})
	mux.HandleFunc("POST /api", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req["devicetype"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if fb.refusals.Add(-1) >= 0 {
			w.Write([]byte(`[{"error":{"type":101,"address":"","description":"link button not pressed"}}]`))
			return
		}
		w.Write([]byte(`[{"success":{"username":"` + fb.appKey + `","clientkey":"00112233445566778899AABBCCDDEEFF"}}]`))
	})
	fb.seedResources()
	mux.HandleFunc("/clip/v2/resource/{type}", fb.v2Handler)
	mux.HandleFunc("/clip/v2/resource/{type}/{id}", fb.v2Handler)
	mux.HandleFunc("GET /clip/v2/resource/bridge", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(appKeyHeader) != fb.appKey {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"errors":[{"description":"unauthorized user"}],"data":[]}`))
			return
		}
		w.Write([]byte(`{"errors":[],"data":[{"id":"bridge-1","type":"bridge","bridge_id":"` + fb.id + `"}]}`))
	})

	fb.Server = httptest.NewUnstartedServer(mux)
	cert := selfSignedBridgeCert(t, strings.ToUpper(fb.id))
	fb.Server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	fb.Server.StartTLS()
	fb.fingerprint = fingerprint(cert.Certificate[0])
	t.Cleanup(fb.Server.Close)
	return fb
}

// addr returns host:port without the https:// prefix.
func (fb *fakeBridge) addr() string {
	return strings.TrimPrefix(fb.URL, "https://")
}

// selfSignedBridgeCert generates a certificate shaped like a bridge's.
func selfSignedBridgeCert(t *testing.T, cn string) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{Country: []string{"NL"}, Organization: []string{"Philips Hue"}, CommonName: cn},
		Issuer:       pkix.Name{CommonName: "root-bridge"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}
