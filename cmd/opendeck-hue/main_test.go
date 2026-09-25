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
// event loop. It returns the host side of the WebSocket and a channel of
// decoded messages the plugin sent.
func startPlugin(t *testing.T) (*websocket.Conn, chan map[string]any) {
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
	return host, sent
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
	host, sent := startPlugin(t)
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
	host, sent := startPlugin(t)
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
	host, sent := startPlugin(t)
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

func TestWillAppearSetsTitleFromSettings(t *testing.T) {
	host, sent := startPlugin(t)
	push(t, host, map[string]any{
		"event": "willAppear", "action": actionPrefix + "toggle-light", "context": "ctx-4",
		"payload": map[string]any{"settings": map[string]any{"target": huetest.LightKitchen, "name": "Kitchen"}, "controller": "Keypad"},
	})
	m := next(t, sent)
	if m["event"] != "setTitle" || m["payload"].(map[string]any)["title"] != "Kitchen" {
		t.Errorf("got %v, want setTitle Kitchen", m)
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
