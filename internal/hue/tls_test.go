package hue

import (
	"context"
	"strings"
	"testing"
)

const testBridgeID = "001788fffe0a0b0c"

func TestClientAcceptsMatchingCertificate(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	c := NewClient(ClientOptions{Addr: fb.addr(), ID: testBridgeID, Log: testLogger(t)})

	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != testBridgeID || info.Model != "BSB002" {
		t.Errorf("info = %+v", info)
	}
	cn, fp, ok := c.SeenCertificate()
	if !ok || cn != testBridgeID || fp != fb.fingerprint {
		t.Errorf("seen = %q %q %v, want %q %q", cn, fp, ok, testBridgeID, fb.fingerprint)
	}
}

func TestClientRejectsWrongBridgeID(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	c := NewClient(ClientOptions{Addr: fb.addr(), ID: "ecb5fafffe000000", Log: testLogger(t)})

	_, err := c.Info(context.Background())
	if err == nil || !strings.Contains(err.Error(), "belongs to bridge "+testBridgeID) {
		t.Fatalf("expected CN mismatch error, got %v", err)
	}
}

func TestClientRejectsChangedCertificate(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	c := NewClient(ClientOptions{Addr: fb.addr(), ID: testBridgeID, Fingerprint: "sha256:deadbeef", Log: testLogger(t)})

	_, err := c.Info(context.Background())
	if err == nil || !strings.Contains(err.Error(), "certificate changed") {
		t.Fatalf("expected pin mismatch error, got %v", err)
	}
}

func TestClientAcceptsPinnedCertificate(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	c := NewClient(ClientOptions{Addr: fb.addr(), ID: testBridgeID, Fingerprint: fb.fingerprint})
	if _, err := c.Info(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestProbeModeAcceptsAnyID(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	c := NewClient(ClientOptions{Addr: fb.addr()}) // no ID: probe before pairing
	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != testBridgeID {
		t.Errorf("probe returned id %q", info.ID)
	}
}

func TestCheckKey(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	good := NewClient(ClientOptions{Addr: fb.addr(), ID: testBridgeID, AppKey: fb.appKey})
	if err := good.CheckKey(context.Background()); err != nil {
		t.Errorf("valid key rejected: %v", err)
	}
	bad := NewClient(ClientOptions{Addr: fb.addr(), ID: testBridgeID, AppKey: "wrong"})
	if err := bad.CheckKey(context.Background()); err == nil || !strings.Contains(err.Error(), "rejected") {
		t.Errorf("invalid key accepted: %v", err)
	}
}
