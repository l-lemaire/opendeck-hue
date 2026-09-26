package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/l-lemaire/opendeck-hue/internal/hue"
	"github.com/l-lemaire/opendeck-hue/internal/openaction"
	"github.com/l-lemaire/opendeck-hue/internal/pairing"
)

// The property inspector (plugin/propertyInspector/index.html) cannot reach
// the bridge itself: it runs in a webview without our TLS pinning or the
// keyring. So it asks the plugin, over the host, with sendToPlugin, and the
// plugin answers with sendToPropertyInspector. Message shapes:
//
//	inspector -> plugin   {"event":"listTargets","bridge":"<id or empty>"}
//	plugin -> inspector   {"event":"targets","bridges":[...],"bridge":"<id>",
//	                       "items":[{"id":..,"name":..,"on":..,"grouped_light":..}]}
//	plugin -> inspector   {"event":"error","code":"not_paired"|"","message":"..."}
//
//	inspector -> plugin   {"event":"discover"}
//	plugin -> inspector   {"event":"bridges","items":[{"id","host","port","name","model","source"}]}
//	                      or {"event":"bridges","items":[],"message":"<why>"}
//	inspector -> plugin   {"event":"pair","address":"<host[:port]>","id":"<bridge id>"}
//	plugin -> inspector   {"event":"pairing","stage":"searching|found|waiting|paired|error",
//	                       "message":"...","seconds_left":N}
//
// Pairing from the panel means a user never needs the CLI: the plugin runs
// the same flow (internal/pairing) and stores the key in the same keyring.

type inspectorRequest struct {
	Event   string `json:"event"`
	Bridge  string `json:"bridge"`
	Address string `json:"address"`
	ID      string `json:"id"`
}

type bridgesReply struct {
	Event   string       `json:"event"`
	Items   []hue.Bridge `json:"items"`
	Message string       `json:"message,omitempty"`
}

type pairingReply struct {
	Event       string `json:"event"`
	Stage       string `json:"stage"`
	Message     string `json:"message"`
	SecondsLeft int    `json:"seconds_left,omitempty"`
}

type targetItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	On           bool   `json:"on"` // for scenes: currently active
	GroupedLight string `json:"grouped_light,omitempty"`
	Group        string `json:"group,omitempty"` // for scenes: the room/zone id
}

// groupItem is a room or zone offered to filter scenes by.
type groupItem struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind"`
}

type bridgeItem struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Default bool   `json:"default"`
}

type targetsReply struct {
	Event   string       `json:"event"`
	Kind    string       `json:"kind"`
	Bridge  string       `json:"bridge"`
	Bridges []bridgeItem `json:"bridges"`
	Items   []targetItem `json:"items"`
	Groups  []groupItem  `json:"groups,omitempty"` // scenes only
}

type errorReply struct {
	Event   string `json:"event"`
	Code    string `json:"code,omitempty"` // "not_paired" tells the panel to offer pairing
	Message string `json:"message"`
}

// errNotPaired marks the "no bridge yet" case so the panel can show the
// Pair button instead of a bare error.
var errNotPaired = errors.New("no bridge paired yet")

// handleInspectorMessage serves sendToPlugin events. Errors are reported to
// the inspector so the user sees them, and returned so they are logged.
func (p *plugin) handleInspectorMessage(ctx context.Context, ev openaction.Event, payload json.RawMessage) error {
	var req inspectorRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("inspector sent invalid JSON: %w", err)
	}
	switch req.Event {
	case "listTargets":
		reply, err := p.listTargets(ctx, ev.Action, req.Bridge)
		if err != nil {
			code := ""
			if errors.Is(err, errNotPaired) {
				code = "not_paired"
			}
			p.conn.SendToPropertyInspector(ctx, ev.Action, ev.Context, errorReply{Event: "error", Code: code, Message: err.Error()})
			return err
		}
		return p.conn.SendToPropertyInspector(ctx, ev.Action, ev.Context, reply)
	case "discover":
		return p.startDiscovery(ctx, ev)
	case "pair":
		return p.startPairing(ctx, ev, req.Address, req.ID)
	default:
		return fmt.Errorf("inspector sent unknown event %q", req.Event)
	}
}

// listTargets fetches the lights, rooms or zones of a bridge, according to
// the action, along with the list of known bridges.
func (p *plugin) listTargets(ctx context.Context, action, bridgeID string) (targetsReply, error) {
	kind, err := kindOf(action)
	if err != nil {
		return targetsReply{}, err
	}
	known, defaultID, err := p.bridges.Bridges()
	if err != nil {
		return targetsReply{}, err
	}
	if len(known) == 0 {
		return targetsReply{}, errNotPaired
	}
	client, bridge, err := p.bridges.Connect(ctx, bridgeID)
	if err != nil {
		return targetsReply{}, err
	}

	reply := targetsReply{Event: "targets", Kind: kind, Bridge: bridge.ID}
	for _, b := range known {
		reply.Bridges = append(reply.Bridges, bridgeItem{ID: b.ID, Name: b.Name, Default: b.ID == defaultID})
	}
	switch kind {
	case "light":
		lights, err := client.Lights(ctx)
		if err != nil {
			return targetsReply{}, err
		}
		for _, l := range lights {
			reply.Items = append(reply.Items, targetItem{ID: l.ID, Name: l.Name(), On: l.IsOn()})
		}
	case "scene":
		// Scenes come with the rooms and zones they belong to, so the panel
		// can offer a group first and then the scenes of that group.
		groups, err := client.Groups(ctx)
		if err != nil {
			return targetsReply{}, err
		}
		for _, g := range groups {
			reply.Groups = append(reply.Groups, groupItem{ID: g.ID, Name: g.Name, Kind: g.Kind})
		}
		scenes, err := client.Scenes(ctx)
		if err != nil {
			return targetsReply{}, err
		}
		for _, sc := range scenes {
			reply.Items = append(reply.Items, targetItem{ID: sc.ID, Name: sc.Name(), On: sc.IsActive(), Group: sc.Group.RID})
		}
	default:
		var groups []hue.Group
		if kind == "room" {
			groups, err = client.Rooms(ctx)
		} else {
			groups, err = client.Zones(ctx)
		}
		if err != nil {
			return targetsReply{}, err
		}
		for _, g := range groups {
			reply.Items = append(reply.Items, targetItem{ID: g.ID, Name: g.Name, On: g.On, GroupedLight: g.GroupedLightID})
		}
	}
	if reply.Items == nil {
		reply.Items = []targetItem{} // "[]" rather than "null" in the JSON
	}
	return reply, nil
}

// startDiscovery looks for bridges in a goroutine (mDNS takes two seconds)
// and sends the list to the panel.
func (p *plugin) startDiscovery(ctx context.Context, ev openaction.Event) error {
	go func() {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		bridges, err := p.bridges.Discover(ctx)
		reply := bridgesReply{Event: "bridges", Items: bridges}
		if reply.Items == nil {
			reply.Items = []hue.Bridge{}
		}
		if err != nil {
			reply.Message = err.Error()
			p.info.Printf("discovery for the panel failed: %v", err)
		} else {
			p.info.Printf("discovery for the panel found %d bridge(s)", len(bridges))
		}
		p.conn.SendToPropertyInspector(ctx, ev.Action, ev.Context, reply)
	}()
	return nil
}

// startPairing runs the pairing flow in a goroutine, forwarding each
// progress step to the panel. Only one pairing runs at a time.
func (p *plugin) startPairing(ctx context.Context, ev openaction.Event, address, id string) error {
	if !p.inflight.begin("pairing") {
		return p.conn.SendToPropertyInspector(ctx, ev.Action, ev.Context,
			pairingReply{Event: "pairing", Stage: "error", Message: "A pairing is already in progress"})
	}
	p.info.Printf("pairing requested from the panel (address %q, id %q)", address, id)
	go func() {
		defer p.inflight.end("pairing")
		ctx, cancel := context.WithTimeout(ctx, pairing.DefaultTimeout+30*time.Second)
		defer cancel()

		bridge, err := p.bridges.Pair(ctx, address, id, func(pr pairing.Progress) {
			p.conn.SendToPropertyInspector(ctx, ev.Action, ev.Context,
				pairingReply{Event: "pairing", Stage: pr.Stage, Message: pr.Message, SecondsLeft: pr.SecondsLeft})
		})
		if err != nil {
			p.info.Printf("pairing failed: %v", err)
			p.conn.SendToPropertyInspector(ctx, ev.Action, ev.Context,
				pairingReply{Event: "pairing", Stage: "error", Message: err.Error()})
			return
		}
		p.info.Printf("paired with bridge %s (%s)", bridge.ID, bridge.Name)
	}()
	return nil
}
