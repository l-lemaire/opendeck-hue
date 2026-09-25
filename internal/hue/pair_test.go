package hue

import (
	"context"
	"errors"
	"github.com/llemaire/streamdeck/internal/hue/huetest"
	"testing"
	"time"
)

func fastPolling(t *testing.T) {
	old := pairPollInterval
	pairPollInterval = 10 * time.Millisecond
	t.Cleanup(func() { pairPollInterval = old })
}

func TestPairWaitsForButton(t *testing.T) {
	fastPolling(t)
	fb := huetest.New(t, testBridgeID)
	fb.Refusals.Store(3) // button "pressed" on the 4th attempt

	c := NewClient(ClientOptions{Addr: fb.Addr(), ID: testBridgeID, Log: testLogger(t)})
	waits := 0
	creds, err := c.Pair(context.Background(), "hue-cli", "test", time.Second, func(int) { waits++ })
	if err != nil {
		t.Fatal(err)
	}
	if creds.AppKey != fb.AppKey || creds.ClientKey == "" {
		t.Errorf("creds = %+v", creds)
	}
	if waits != 3 {
		t.Errorf("onWaiting called %d times, want 3", waits)
	}
}

func TestPairTimesOut(t *testing.T) {
	fastPolling(t)
	fb := huetest.New(t, testBridgeID)
	fb.Refusals.Store(1000)

	c := NewClient(ClientOptions{Addr: fb.Addr(), ID: testBridgeID})
	_, err := c.Pair(context.Background(), "hue-cli", "test", 50*time.Millisecond, nil)
	if !errors.Is(err, ErrLinkButtonNotPressed) {
		t.Fatalf("got %v, want ErrLinkButtonNotPressed", err)
	}
}

func TestPairStopsOnCancel(t *testing.T) {
	fastPolling(t)
	fb := huetest.New(t, testBridgeID)
	fb.Refusals.Store(1000)

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(30 * time.Millisecond)
		cancel()
	}()
	c := NewClient(ClientOptions{Addr: fb.Addr(), ID: testBridgeID})
	_, err := c.Pair(ctx, "hue-cli", "test", 10*time.Second, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v, want context.Canceled", err)
	}
}

func TestValidateDeviceType(t *testing.T) {
	cases := []struct {
		app, device string
		ok          bool
	}{
		{"hue-cli", "fedora", true},
		{"", "fedora", false},
		{"123456789012345678901", "x", false}, // 21 chars
		{"hue-cli", "12345678901234567890", false},
		{"hue#cli", "x", false},
	}
	for _, c := range cases {
		err := validateDeviceType(c.app, c.device)
		if (err == nil) != c.ok {
			t.Errorf("validateDeviceType(%q, %q) = %v, want ok=%v", c.app, c.device, err, c.ok)
		}
	}
}
