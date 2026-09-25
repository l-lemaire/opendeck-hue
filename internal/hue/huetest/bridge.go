// Package huetest provides a fake Hue bridge for tests: an HTTPS server
// with a bridge-shaped certificate that implements the endpoints the hue
// package uses, with mutable light, room and zone state.
//
// It lives in its own package (instead of a _test.go file in package hue) so
// that tests of other packages, such as the OpenDeck plugin, can use it.
package huetest

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
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

// AppKeyHeader mirrors the header name the hue package sends.
const AppKeyHeader = "hue-application-key"

// Bridge is an HTTPS server that behaves like a Hue bridge for the
// endpoints the hue package uses. Its certificate mimics a real one: CN is
// the bridge id in upper case, issuer "root-bridge", no SAN.
type Bridge struct {
	*httptest.Server
	// ID is the lower-case bridge id.
	ID string
	// Fingerprint is "sha256:<hex>" of the certificate, as the hue package
	// computes it.
	Fingerprint string
	// Refusals is how many pairing attempts return "link button not
	// pressed" before one succeeds. atomic because the server handles
	// requests on other goroutines.
	Refusals atomic.Int32
	// AppKey is the only application key the fake accepts.
	AppKey string

	// Mutable v2 resources, keyed by id. mu guards them because the HTTP
	// server handles each request on its own goroutine.
	mu      sync.Mutex
	lights  map[string]map[string]any
	grouped map[string]map[string]any
	rooms   []map[string]any
	zones   []map[string]any
	puts    []string // "type/id" of every PUT, in order, for assertions
}

// Puts returns "type/id" for every PUT received so far, in order.
func (fb *Bridge) Puts() []string {
	fb.mu.Lock()
	defer fb.mu.Unlock()
	return append([]string(nil), fb.puts...)
}

// Fixed ids of the seeded resources so tests can refer to them.
const (
	LightKitchen   = "11111111-1111-1111-1111-111111111111"
	LightDesk      = "22222222-2222-2222-2222-222222222222"
	LightPlug      = "33333333-3333-3333-3333-333333333333"
	GroupedKitchen = "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"
	GroupedOffice  = "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb"
	RoomKitchen    = "cccccccc-cccc-cccc-cccc-cccccccccccc"
	ZoneOffice     = "dddddddd-dddd-dddd-dddd-dddddddddddd"
)

func (fb *Bridge) seedResources() {
	fb.lights = map[string]map[string]any{
		LightKitchen: {"id": LightKitchen, "id_v1": "/lights/1", "type": "light",
			"metadata": map[string]any{"name": "Kitchen", "archetype": "sultan_bulb"},
			"on":       map[string]any{"on": true}, "dimming": map[string]any{"brightness": 80.0}},
		LightDesk: {"id": LightDesk, "id_v1": "/lights/2", "type": "light",
			"metadata": map[string]any{"name": "Desk lamp", "archetype": "spot_bulb"},
			"on":       map[string]any{"on": false}, "dimming": map[string]any{"brightness": 50.0}},
		LightPlug: {"id": LightPlug, "id_v1": "/lights/3", "type": "light",
			"metadata": map[string]any{"name": "Desk plug", "archetype": "plug"},
			"on":       map[string]any{"on": false}},
	}
	fb.grouped = map[string]map[string]any{
		GroupedKitchen: {"id": GroupedKitchen, "type": "grouped_light", "owner": map[string]any{"rid": RoomKitchen, "rtype": "room"},
			"on": map[string]any{"on": true}, "dimming": map[string]any{"brightness": 80.0}},
		GroupedOffice: {"id": GroupedOffice, "type": "grouped_light", "owner": map[string]any{"rid": ZoneOffice, "rtype": "zone"},
			"on": map[string]any{"on": false}, "dimming": map[string]any{"brightness": 50.0}},
	}
	fb.rooms = []map[string]any{{
		"id": RoomKitchen, "type": "room", "metadata": map[string]any{"name": "Kitchen"},
		"children": []map[string]any{{"rid": "dev-1", "rtype": "device"}},
		"services": []map[string]any{{"rid": GroupedKitchen, "rtype": "grouped_light"}},
	}}
	fb.zones = []map[string]any{{
		"id": ZoneOffice, "type": "zone", "metadata": map[string]any{"name": "Office"},
		"children": []map[string]any{{"rid": LightDesk, "rtype": "light"}, {"rid": LightPlug, "rtype": "light"}},
		"services": []map[string]any{{"rid": GroupedOffice, "rtype": "grouped_light"}},
	}}
}

// v2Handler serves /clip/v2/resource/{type} and {type}/{id} for the seeded
// resources, requiring the application key like the real bridge.
func (fb *Bridge) v2Handler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get(AppKeyHeader) != fb.AppKey {
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

// New starts a fake bridge with the given id and seeded resources. It is
// closed automatically when the test ends.
func New(t *testing.T, id string) *Bridge {
	t.Helper()
	fb := &Bridge{ID: strings.ToLower(id), AppKey: "fake-app-key-0123456789"}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/0/config", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]string{
			"name": "Fake Bridge", "bridgeid": strings.ToUpper(fb.ID), "modelid": "BSB002",
			"apiversion": "1.78.0", "swversion": "1978293000",
		})
	})
	mux.HandleFunc("POST /api", func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req["devicetype"] == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if fb.Refusals.Add(-1) >= 0 {
			w.Write([]byte(`[{"error":{"type":101,"address":"","description":"link button not pressed"}}]`))
			return
		}
		w.Write([]byte(`[{"success":{"username":"` + fb.AppKey + `","clientkey":"00112233445566778899AABBCCDDEEFF"}}]`))
	})
	fb.seedResources()
	mux.HandleFunc("/clip/v2/resource/{type}", fb.v2Handler)
	mux.HandleFunc("/clip/v2/resource/{type}/{id}", fb.v2Handler)
	mux.HandleFunc("GET /clip/v2/resource/bridge", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get(AppKeyHeader) != fb.AppKey {
			w.WriteHeader(http.StatusUnauthorized)
			w.Write([]byte(`{"errors":[{"description":"unauthorized user"}],"data":[]}`))
			return
		}
		w.Write([]byte(`{"errors":[],"data":[{"id":"bridge-1","type":"bridge","bridge_id":"` + fb.ID + `"}]}`))
	})

	fb.Server = httptest.NewUnstartedServer(mux)
	cert := selfSignedBridgeCert(t, strings.ToUpper(fb.ID))
	fb.Server.TLS = &tls.Config{Certificates: []tls.Certificate{cert}}
	fb.Server.StartTLS()
	sum := sha256.Sum256(cert.Certificate[0])
	fb.Fingerprint = "sha256:" + hex.EncodeToString(sum[:])
	t.Cleanup(fb.Server.Close)
	return fb
}

// Addr returns host:port without the https:// prefix.
func (fb *Bridge) Addr() string {
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
