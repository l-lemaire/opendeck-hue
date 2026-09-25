package hue

import (
	"context"
	"testing"
	"time"

	"github.com/l-lemaire/opendeck-hue/internal/hue/huetest"
)

func TestStreamEvents(t *testing.T) {
	fb := huetest.New(t, testBridgeID)
	c := pairedClient(t, fb)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connected := make(chan struct{})
	changes := make(chan Change, 16)
	done := make(chan error, 1)
	go func() {
		done <- c.StreamEvents(ctx, EventHandler{
			OnConnect: func() { close(connected) },
			OnChange:  func(ch Change) { changes <- ch },
		})
	}()

	select {
	case <-connected:
	case <-time.After(3 * time.Second):
		t.Fatal("stream never connected")
	}

	// A change made "from the Hue app"...
	fb.SetLightOn(huetest.LightKitchen, false)
	// ...and one made through the API (PUT) both produce events.
	if err := c.SetLight(ctx, huetest.LightDesk, Update{On: Bool(true)}); err != nil {
		t.Fatal(err)
	}
	// An unrelated batch type is ignored.
	fb.Emit(`[{"type":"add","data":[{"id":"new-scene","type":"scene"}]}]`)
	// Garbage does not kill the stream.
	fb.Emit(`not json`)
	fb.SetGroupedLightOn(huetest.GroupedOffice, true)

	want := []Change{
		{ResourceID: huetest.LightKitchen, ResourceType: "light", On: Bool(false)},
		{ResourceID: huetest.LightDesk, ResourceType: "light", On: Bool(true)},
		{ResourceID: huetest.GroupedOffice, ResourceType: "grouped_light", On: Bool(true)},
	}
	for i, w := range want {
		select {
		case got := <-changes:
			if got.ResourceID != w.ResourceID || got.ResourceType != w.ResourceType || got.On == nil || *got.On != *w.On {
				t.Errorf("change %d = %+v, want %+v", i, got, w)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("change %d never arrived", i)
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("StreamEvents returned %v after cancel, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("StreamEvents did not return after cancel")
	}
}

func TestStreamEventsRejectsBadKey(t *testing.T) {
	fb := huetest.New(t, testBridgeID)
	c := NewClient(ClientOptions{Addr: fb.Addr(), ID: fb.ID, AppKey: "wrong"})
	if err := c.StreamEvents(context.Background(), EventHandler{}); err == nil {
		t.Fatal("expected an error")
	}
}

func TestWatchReconnects(t *testing.T) {
	fb := huetest.New(t, testBridgeID)
	c := pairedClient(t, fb)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connects := make(chan struct{}, 4)
	errs := make(chan error, 4)
	go c.Watch(ctx, EventHandler{
		OnConnect: func() { connects <- struct{}{} },
		OnError:   func(err error, _ time.Duration) { errs <- err },
	})
	<-connects
	// Kill the fake bridge's connections: the stream breaks, Watch retries.
	fb.CloseClientConnections()
	select {
	case <-errs:
	case <-time.After(3 * time.Second):
		t.Fatal("no error reported after the connection dropped")
	}
	select {
	case <-connects:
	case <-time.After(5 * time.Second):
		t.Fatal("did not reconnect")
	}
}
