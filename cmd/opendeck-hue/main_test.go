package main

import (
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/llemaire/streamdeck/internal/config"
	"github.com/llemaire/streamdeck/internal/hue"
	"github.com/llemaire/streamdeck/internal/hue/huetest"
	"github.com/llemaire/streamdeck/internal/openaction"
)

// fakeConnector points the plugin at a huetest bridge, bypassing the config
// file and the keyring.
type fakeConnector struct {
	fb     *huetest.Bridge
	client *hue.Client
}

func (f *fakeConnector) Connect(ctx context.Context, id string) (*hue.Client, config.Bridge, error) {
	return f.client, config.Bridge{ID: f.fb.ID, Host: "fake", Port: 443, Name: "Fake Bridge"}, nil
}

func (f *fakeConnector) Bridges() ([]config.Bridge, string, error) {
	return []config.Bridge{{ID: f.fb.ID, Name: "Fake Bridge"}}, f.fb.ID, nil
}

// startPlugin wires a plugin to a fake host and a fake bridge and runs its
// event loop. It returns the host side of the WebSocket, a channel of
// decoded messages the plugin sent, and the fake bridge.
func startPlugin(t *testing.T) (*websocket.Conn, chan map[string]any, *huetest.Bridge) {
	t.Helper()
	fb := huetest.New(t, "001788fffe000001")
	client := hue.NewClient(hue.ClientOptions{Addr: fb.Addr(), ID: fb.ID, Fingerprint: fb.Fingerprint, AppKey: fb.AppKey})

	hostConn := make(chan *websocket.Conn, 1)
	sent := make(chan map[string]any, 64)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		hostConn <- c
		for {
			_, data, err := c.Read(context.Background())
			if err != nil {
				return
			}
			var m map[string]any
			json.Unmarshal(data, &m)
			sent <- m
		}
	}))
	t.Cleanup(srv.Close)
	port, _ := strconv.Atoi(strings.TrimPrefix(srv.URL, "http://127.0.0.1:"))

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var debug *log.Logger
	if testing.Verbose() {
		debug = log.New(os.Stderr, "    debug: ", 0)
	}
	conn, err := openaction.Connect(ctx, openaction.Args{Port: port, PluginUUID: "com.github.llemaire.hue.sdPlugin", RegisterEvent: "registerPlugin"}, debug)
	if err != nil {
		t.Fatal(err)
	}
	p := &plugin{conn: conn, info: log.New(io.Discard, "", 0), debug: debug, bridges: &fakeConnector{fb: fb, client: client}}
	go conn.Run(ctx, p.handlers())

	host := <-hostConn
	if reg := next(t, sent); reg["event"] != "registerPlugin" {
		t.Fatalf("first message = %v", reg)
	}
	return host, sent, fb
}

// expect pulls messages until one with the given event arrives, failing on
// anything else. Goroutines make the order of setTitle/setState non-fixed.
func expectEvent(t *testing.T, ch chan map[string]any, event string) map[string]any {
	t.Helper()
	m := next(t, ch)
	if m["event"] != event {
		t.Fatalf("got %v, want event %q", m, event)
	}
	return m
}

func stateOf(m map[string]any) int {
	return int(m["payload"].(map[string]any)["state"].(float64))
}

func next(t *testing.T, ch chan map[string]any) map[string]any {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(3 * time.Second):
		t.Fatal("plugin sent nothing within 3s")
		return nil
	}
}

func push(t *testing.T, host *websocket.Conn, v any) {
	t.Helper()
	data, _ := json.Marshal(v)
	if err := host.Write(context.Background(), websocket.MessageText, data); err != nil {
		t.Fatal(err)
	}
}

func TestInspectorListsLights(t *testing.T) {
	host, sent, _ := startPlugin(t)
	push(t, host, map[string]any{
		"event": "sendToPlugin", "action": actionPrefix + "toggle-light", "context": "ctx-1",
		"payload": map[string]any{"event": "listTargets", "bridge": ""},
	})
	m := next(t, sent)
	if m["event"] != "sendToPropertyInspector" || m["context"] != "ctx-1" {
		t.Fatalf("reply = %v", m)
	}
	payload := m["payload"].(map[string]any)
	if payload["event"] != "targets" || payload["kind"] != "light" {
		t.Errorf("payload = %v", payload)
	}
	items := payload["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("got %d items, want 3", len(items))
	}
	first := items[0].(map[string]any)
	if first["name"] != "Desk lamp" || first["id"] != huetest.LightDesk {
		t.Errorf("first item = %v", first)
	}
	if bridges := payload["bridges"].([]any); len(bridges) != 1 || bridges[0].(map[string]any)["default"] != true {
		t.Errorf("bridges = %v", payload["bridges"])
	}
}

func TestInspectorListsZonesWithGroupedLight(t *testing.T) {
	host, sent, _ := startPlugin(t)
	push(t, host, map[string]any{
		"event": "sendToPlugin", "action": actionPrefix + "toggle-zone", "context": "ctx-2",
		"payload": map[string]any{"event": "listTargets"},
	})
	payload := next(t, sent)["payload"].(map[string]any)
	items := payload["items"].([]any)
	if payload["kind"] != "zone" || len(items) != 1 {
		t.Fatalf("payload = %v", payload)
	}
	office := items[0].(map[string]any)
	if office["name"] != "Office" || office["grouped_light"] != huetest.GroupedOffice {
		t.Errorf("office = %v", office)
	}
}

func TestInspectorErrors(t *testing.T) {
	host, sent, _ := startPlugin(t)
	// Unknown action -> error reply to the inspector.
	push(t, host, map[string]any{
		"event": "sendToPlugin", "action": actionPrefix + "dimmer", "context": "ctx-3",
		"payload": map[string]any{"event": "listTargets"},
	})
	payload := next(t, sent)["payload"].(map[string]any)
	if payload["event"] != "error" || !strings.Contains(payload["message"].(string), "unknown action") {
		t.Errorf("payload = %v", payload)
	}
}

func TestWillAppearSetsTitleAndState(t *testing.T) {
	host, sent, _ := startPlugin(t)
	push(t, host, map[string]any{
		"event": "willAppear", "action": actionPrefix + "toggle-light", "context": "ctx-4",
		"payload": map[string]any{"settings": map[string]any{"target": huetest.LightKitchen, "name": "Kitchen"}, "controller": "Keypad"},
	})
	title := expectEvent(t, sent, "setTitle")
	if title["payload"].(map[string]any)["title"] != "Kitchen" {
		t.Errorf("title = %v", title)
	}
	// Kitchen is on in the fake bridge, so the refreshed state is 1.
	if st := expectEvent(t, sent, "setState"); stateOf(st) != stateOn {
		t.Errorf("state = %v, want on", st)
	}
}

func TestWillAppearWithoutTargetDoesNothing(t *testing.T) {
	host, sent, _ := startPlugin(t)
	push(t, host, map[string]any{
		"event": "willAppear", "action": actionPrefix + "toggle-light", "context": "ctx-5",
		"payload": map[string]any{"settings": map[string]any{}, "controller": "Keypad"},
	})
	select {
	case m := <-sent:
		t.Fatalf("unexpected message %v", m)
	case <-time.After(300 * time.Millisecond):
	}
}

func keyDown(action, context string, settings map[string]any) map[string]any {
	return map[string]any{
		"event": "keyDown", "action": actionPrefix + action, "context": context,
		"payload": map[string]any{"settings": settings, "state": 0},
	}
}

func TestKeyDownTogglesLight(t *testing.T) {
	host, sent, fb := startPlugin(t)
	settings := map[string]any{"target": huetest.LightKitchen, "name": "Kitchen", "kind": "light"}

	push(t, host, keyDown("toggle-light", "ctx-6", settings)) // on -> off
	if st := expectEvent(t, sent, "setState"); stateOf(st) != stateOff {
		t.Errorf("after first press state = %v, want off", st)
	}
	push(t, host, keyDown("toggle-light", "ctx-6", settings)) // off -> on
	if st := expectEvent(t, sent, "setState"); stateOf(st) != stateOn {
		t.Errorf("after second press state = %v, want on", st)
	}
	if puts := fb.Puts(); len(puts) != 2 || puts[0] != "light/"+huetest.LightKitchen {
		t.Errorf("bridge PUTs = %v", puts)
	}
}

func TestKeyDownTogglesZoneViaGroupedLight(t *testing.T) {
	host, sent, fb := startPlugin(t)
	// Settings written by the inspector carry grouped_light; no lookup needed.
	push(t, host, keyDown("toggle-zone", "ctx-7", map[string]any{
		"target": huetest.ZoneOffice, "grouped_light": huetest.GroupedOffice, "name": "Office", "kind": "zone",
	}))
	if st := expectEvent(t, sent, "setState"); stateOf(st) != stateOn { // office was off
		t.Errorf("state = %v, want on", st)
	}
	if puts := fb.Puts(); len(puts) != 1 || puts[0] != "grouped_light/"+huetest.GroupedOffice {
		t.Errorf("bridge PUTs = %v", puts)
	}
}

func TestKeyDownRoomWithoutGroupedLightLooksItUp(t *testing.T) {
	host, sent, fb := startPlugin(t)
	push(t, host, keyDown("toggle-room", "ctx-8", map[string]any{"target": huetest.RoomKitchen, "name": "Kitchen"}))
	if st := expectEvent(t, sent, "setState"); stateOf(st) != stateOff { // kitchen room was on
		t.Errorf("state = %v, want off", st)
	}
	if puts := fb.Puts(); len(puts) != 1 || puts[0] != "grouped_light/"+huetest.GroupedKitchen {
		t.Errorf("bridge PUTs = %v", puts)
	}
}

func TestKeyDownWithoutTargetAlerts(t *testing.T) {
	host, sent, fb := startPlugin(t)
	push(t, host, keyDown("toggle-light", "ctx-9", map[string]any{}))
	expectEvent(t, sent, "showAlert")
	if len(fb.Puts()) != 0 {
		t.Error("bridge was written to")
	}
}

func TestKeyDownOnMissingLightAlerts(t *testing.T) {
	host, sent, fb := startPlugin(t)
	push(t, host, keyDown("toggle-light", "ctx-10", map[string]any{"target": "gone-gone-gone", "name": "Old"}))
	expectEvent(t, sent, "showAlert")
	if len(fb.Puts()) != 0 {
		t.Error("bridge was written to")
	}
}

func TestSettingsHelpers(t *testing.T) {
	if k, err := kindOf(actionPrefix + "toggle-room"); err != nil || k != "room" {
		t.Errorf("kindOf room = %q, %v", k, err)
	}
	if _, err := kindOf("com.other.thing"); err == nil {
		t.Error("foreign action should be rejected")
	}
	s, err := decodeSettings(json.RawMessage(`{"target":"abc","name":"Kitchen","grouped_light":"g1"}`))
	if err != nil || s.Target != "abc" || s.GroupedLight != "g1" {
		t.Errorf("decodeSettings = %+v, %v", s, err)
	}
	if s, err := decodeSettings(nil); err != nil || s != (Settings{}) {
		t.Errorf("empty settings = %+v, %v", s, err)
	}
	if _, err := decodeSettings(json.RawMessage(`"nope"`)); err == nil {
		t.Error("non-object settings should fail")
	}
}

// waitForState drains messages until a setState for the button with the
// wanted state arrives. Refreshes may produce extra setState messages with
// the previous state first, so exact sequencing is not asserted.
func waitForState(t *testing.T, ch chan map[string]any, buttonContext string, want int) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case m := <-ch:
			if m["event"] == "setState" && m["context"] == buttonContext && stateOf(m) == want {
				return
			}
		case <-deadline:
			t.Fatalf("no setState %d for %s within 5s", want, buttonContext)
		}
	}
}

func TestExternalChangeUpdatesButtons(t *testing.T) {
	host, sent, fb := startPlugin(t)
	push(t, host, map[string]any{
		"event": "willAppear", "action": actionPrefix + "toggle-light", "context": "key-a",
		"payload": map[string]any{"settings": map[string]any{"target": huetest.LightKitchen, "name": "Kitchen", "kind": "light"}},
	})
	push(t, host, map[string]any{
		"event": "willAppear", "action": actionPrefix + "toggle-zone", "context": "key-b",
		"payload": map[string]any{"settings": map[string]any{"target": huetest.ZoneOffice, "grouped_light": huetest.GroupedOffice, "name": "Office", "kind": "zone"}},
	})
	waitForState(t, sent, "key-a", stateOn)  // initial refresh: kitchen on
	waitForState(t, sent, "key-b", stateOff) // office off

	// Changes made outside the plugin, as from the Hue app.
	fb.SetLightOn(huetest.LightKitchen, false)
	waitForState(t, sent, "key-a", stateOff)
	fb.SetGroupedLightOn(huetest.GroupedOffice, true)
	waitForState(t, sent, "key-b", stateOn)

	// A removed button is no longer updated.
	push(t, host, map[string]any{"event": "willDisappear", "action": actionPrefix + "toggle-light", "context": "key-a", "payload": map[string]any{}})
	time.Sleep(100 * time.Millisecond)
	fb.SetLightOn(huetest.LightKitchen, true)
	fb.SetGroupedLightOn(huetest.GroupedOffice, false) // key-b still updates, proving the stream is alive
	waitForState(t, sent, "key-b", stateOff)
	select {
	case m := <-sent:
		if m["context"] == "key-a" {
			t.Errorf("removed button was updated: %v", m)
		}
	case <-time.After(200 * time.Millisecond):
	}
}
