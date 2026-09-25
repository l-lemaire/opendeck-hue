package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/llemaire/streamdeck/internal/hue"
	"github.com/llemaire/streamdeck/internal/openaction"
)

// The property inspector (plugin/propertyInspector/index.html) cannot reach
// the bridge itself: it runs in a webview without our TLS pinning or the
// keyring. So it asks the plugin, over the host, with sendToPlugin, and the
// plugin answers with sendToPropertyInspector. Message shapes:
//
//	inspector -> plugin   {"event":"listTargets","bridge":"<id or empty>"}
//	plugin -> inspector   {"event":"targets","bridges":[...],"bridge":"<id>",
//	                       "items":[{"id":..,"name":..,"on":..,"grouped_light":..}]}
//	plugin -> inspector   {"event":"error","message":"..."}

type inspectorRequest struct {
	Event  string `json:"event"`
	Bridge string `json:"bridge"`
}

type targetItem struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	On           bool   `json:"on"`
	GroupedLight string `json:"grouped_light,omitempty"`
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
}

type errorReply struct {
	Event   string `json:"event"`
	Message string `json:"message"`
}

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
			p.conn.SendToPropertyInspector(ctx, ev.Action, ev.Context, errorReply{Event: "error", Message: err.Error()})
			return err
		}
		return p.conn.SendToPropertyInspector(ctx, ev.Action, ev.Context, reply)
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
		return targetsReply{}, fmt.Errorf("no bridge paired: run `hue auth` in a terminal first")
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
