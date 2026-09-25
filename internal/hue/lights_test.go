package hue

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func pairedClient(t *testing.T, fb *fakeBridge) *Client {
	return NewClient(ClientOptions{Addr: fb.addr(), ID: fb.id, Fingerprint: fb.fingerprint, AppKey: fb.appKey, Log: testLogger(t)})
}

func TestLightsList(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	lights, err := pairedClient(t, fb).Lights(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(lights) != 3 {
		t.Fatalf("got %d lights, want 3", len(lights))
	}
	// Sorted by name: "Desk lamp", "Desk plug", "Kitchen".
	if lights[0].Name() != "Desk lamp" || lights[2].Name() != "Kitchen" {
		t.Errorf("order: %s, %s, %s", lights[0].Name(), lights[1].Name(), lights[2].Name())
	}
	k := lights[2]
	if b, ok := k.Brightness(); !k.IsOn() || !ok || b != 80 {
		t.Errorf("kitchen = on:%v bright:%v,%v", k.IsOn(), b, ok)
	}
	if _, ok := lights[1].Brightness(); ok {
		t.Error("plug should not be dimmable")
	}
}

func TestToggleLight(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	c := pairedClient(t, fb)
	ctx := context.Background()

	nowOn, err := c.ToggleLight(ctx, lightKitchen)
	if err != nil || nowOn {
		t.Fatalf("first toggle: on=%v err=%v, want off", nowOn, err)
	}
	nowOn, err = c.ToggleLight(ctx, lightKitchen)
	if err != nil || !nowOn {
		t.Fatalf("second toggle: on=%v err=%v, want on", nowOn, err)
	}
	if len(fb.puts) != 2 || fb.puts[0] != "light/"+lightKitchen {
		t.Errorf("puts = %v", fb.puts)
	}
}

func TestSetLightBrightness(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	c := pairedClient(t, fb)
	ctx := context.Background()

	if err := c.SetLight(ctx, lightDesk, Update{On: Bool(true), Brightness: Float(25)}); err != nil {
		t.Fatal(err)
	}
	l, _ := c.Light(ctx, lightDesk)
	if b, _ := l.Brightness(); !l.IsOn() || b != 25 {
		t.Errorf("desk = on:%v bright:%v", l.IsOn(), b)
	}
	// The plug cannot dim; the bridge answers HTTP 200 with an error entry.
	err := c.SetLight(ctx, lightPlug, Update{Brightness: Float(50)})
	if err == nil || !strings.Contains(err.Error(), "bridge said") {
		t.Errorf("expected envelope error, got %v", err)
	}
}

func TestV2Errors(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	ctx := context.Background()

	if _, err := pairedClient(t, fb).Light(ctx, "no-such-id"); err == nil || !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("missing light: %v", err)
	}
	bad := NewClient(ClientOptions{Addr: fb.addr(), ID: fb.id, AppKey: "wrong"})
	if _, err := bad.Lights(ctx); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Errorf("bad key: %v", err)
	}
}

func TestGroups(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	c := pairedClient(t, fb)
	ctx := context.Background()

	groups, err := c.Groups(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(groups) != 2 || groups[0].Name != "Kitchen" || groups[1].Name != "Office" {
		t.Fatalf("groups = %+v", groups)
	}
	kitchen, office := groups[0], groups[1]
	if kitchen.Kind != "room" || !kitchen.On || kitchen.Members != 1 || kitchen.GroupedLightID != groupedKitchn {
		t.Errorf("kitchen = %+v", kitchen)
	}
	if office.Kind != "zone" || office.On || office.Members != 2 || *office.Brightness != 50 {
		t.Errorf("office = %+v", office)
	}

	nowOn, err := c.ToggleGroup(ctx, office)
	if err != nil || !nowOn {
		t.Fatalf("toggle office: %v %v", nowOn, err)
	}
	if err := c.SetGroup(ctx, Group{Name: "Empty", Kind: "room"}, Update{On: Bool(true)}); err == nil {
		t.Error("group without lights should be refused before any request")
	}
}

func TestDryRunNeverWrites(t *testing.T) {
	fb := newFakeBridge(t, testBridgeID)
	var out bytes.Buffer
	c := NewClient(ClientOptions{Addr: fb.addr(), ID: fb.id, AppKey: fb.appKey, DryRun: &out})
	ctx := context.Background()

	nowOn, err := c.ToggleLight(ctx, lightKitchen)
	if err != nil || nowOn {
		t.Fatalf("dry toggle: on=%v err=%v", nowOn, err)
	}
	if err := c.SetGroup(ctx, Group{Name: "K", Kind: "room", GroupedLightID: groupedKitchn}, Update{Brightness: Float(10)}); err != nil {
		t.Fatal(err)
	}
	if len(fb.puts) != 0 {
		t.Errorf("dry run reached the bridge: %v", fb.puts)
	}
	text := out.String()
	for _, want := range []string{"would send PUT", "/clip/v2/resource/light/" + lightKitchen, `"on": false`, "grouped_light/" + groupedKitchn, `"brightness": 10`} {
		if !strings.Contains(text, want) {
			t.Errorf("dry-run output missing %q:\n%s", want, text)
		}
	}
	// Reads still went through.
	if l, _ := c.Light(ctx, lightKitchen); !l.IsOn() {
		t.Error("state changed or read failed")
	}
}
