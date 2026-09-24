package hue

import (
	"context"
	"encoding/binary"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"
)

// testLogger returns a logger only when tests run with -v, so failures show
// the debug trail without cluttering normal runs.
func testLogger(t *testing.T) *log.Logger {
	if testing.Verbose() {
		return log.New(os.Stderr, "    debug: ", 0)
	}
	return nil
}

// useFakeMDNS makes MDNS use a plain localhost socket instead of the
// multicast group, and returns the socket a fake responder should read from.
// t.Cleanup registers a function to run when the test ends; here it restores
// the package variables so other tests see production behaviour.
func useFakeMDNS(t *testing.T) *net.UDPConn {
	t.Helper()
	responder, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { responder.Close() })

	oldDest, oldListen := mdnsDestination, mdnsListen
	mdnsDestination = responder.LocalAddr().(*net.UDPAddr)
	mdnsListen = func(*net.Interface) (*net.UDPConn, error) {
		return net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 0})
	}
	t.Cleanup(func() { mdnsDestination, mdnsListen = oldDest, oldListen })
	return responder
}

// TestMDNS runs the real query code against a fake responder on localhost.
func TestMDNS(t *testing.T) {
	responder := useFakeMDNS(t)

	// The responder runs in a goroutine (a lightweight thread started with
	// the `go` keyword) so it can wait for the query while MDNS runs.
	go func() {
		buf := make([]byte, 1500)
		n, from, err := responder.ReadFromUDP(buf)
		if err != nil {
			return
		}
		// Sanity-check the question: QDCOUNT == 1 and type PTR at the end.
		if n < dnsHeaderLen || binary.BigEndian.Uint16(buf[4:6]) != 1 {
			return
		}
		responder.WriteToUDP(buf[:n], from) // echo the question: must be ignored (not a response)
		answer := fakeBridgeAnswer(t)
		responder.WriteToUDP(answer, from) // once ...
		responder.WriteToUDP(answer, from) // ... and again, to test de-duplication
	}()

	d := Discovery{Log: testLogger(t), MDNSTimeout: 500 * time.Millisecond}
	bridges, err := d.MDNS(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bridges) != 1 {
		t.Fatalf("got %d bridges, want 1: %+v", len(bridges), bridges)
	}
	if bridges[0].ID != "ecb5fafffe1a2b3c" || bridges[0].Host != "192.168.1.42" {
		t.Errorf("bridge = %+v", bridges[0])
	}
}

func TestMDNSTimeoutWithNoResponder(t *testing.T) {
	useFakeMDNS(t) // a responder that never answers

	start := time.Now()
	bridges, err := Discovery{MDNSTimeout: 200 * time.Millisecond}.MDNS(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bridges) != 0 {
		t.Errorf("expected no bridges, got %+v", bridges)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond || elapsed > 2*time.Second {
		t.Errorf("timeout not respected: took %s", elapsed)
	}
}

func TestCloud(t *testing.T) {
	// httptest.NewServer starts a real HTTP server on localhost for the
	// duration of the test. The handler is an inline function.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`[{"id":"ecb5fafffe1a2b3c","internalipaddress":"192.168.1.42","port":443},
		                 {"id":"001788fffe000001","internalipaddress":"192.168.1.43"}]`))
	}))
	defer srv.Close()

	d := Discovery{Log: testLogger(t), CloudURL: srv.URL}
	bridges, err := d.Cloud(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(bridges) != 2 {
		t.Fatalf("got %d bridges, want 2", len(bridges))
	}
	// Sorted by id, so the 0017... bridge comes first; missing port -> 443.
	if bridges[0].ID != "001788fffe000001" || bridges[0].Port != 443 || bridges[0].Source != "cloud" {
		t.Errorf("bridge[0] = %+v", bridges[0])
	}
	if bridges[1].Addr() != "192.168.1.42:443" {
		t.Errorf("bridge[1].Addr() = %q", bridges[1].Addr())
	}
}

func TestCloudRateLimited(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	_, err := Discovery{CloudURL: srv.URL}.Cloud(context.Background())
	if err == nil {
		t.Fatal("expected an error on HTTP 429")
	}
}
