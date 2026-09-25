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
