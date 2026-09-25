package main

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/l-lemaire/streamdeck/internal/hue"
	"github.com/l-lemaire/streamdeck/internal/openaction"
)

// bridgeTimeout caps one round trip to the bridge for a key press or a
// state refresh. The bridge answers in well under a second on a LAN.
const bridgeTimeout = 10 * time.Second

// States as indexed in the manifest's States arrays.
const (
	stateOff = 0
	stateOn  = 1
)

// onKeyDown toggles the button's target. The bridge call runs in its own
// goroutine so the event loop keeps serving other buttons; the result is
// reported back through SetState or ShowAlert when it arrives.
func (p *plugin) onKeyDown(ctx context.Context, ev openaction.Event, raw json.RawMessage) error {
	s, err := decodeSettings(raw)
	if err != nil {
		p.conn.ShowAlert(ctx, ev.Context)
		return err
	}
	if s.Target == "" {
		p.info.Printf("key %s pressed but no %s chosen yet", ev.Context, shortAction(ev.Action))
		return p.conn.ShowAlert(ctx, ev.Context)
	}
	if !p.inflight.begin(ev.Context) {
		p.info.Printf("key %s pressed while a toggle is still in progress; ignored", ev.Context)
		return nil
	}

	go func() {
		defer p.inflight.end(ev.Context)
		ctx, cancel := context.WithTimeout(ctx, bridgeTimeout)
		defer cancel()

		nowOn, err := p.toggle(ctx, ev.Action, s)
		if err != nil {
			p.info.Printf("toggle %s %q failed: %v", s.Kind, s.Name, err)
			p.conn.ShowAlert(ctx, ev.Context)
			return
		}
		p.info.Printf("toggled %s %q -> %s", s.Kind, s.Name, onOff(nowOn))
		if err := p.conn.SetState(ctx, ev.Context, stateIndex(nowOn)); err != nil {
			p.info.Printf("setState for %s failed: %v", ev.Context, err)
		}
	}()
	return nil
}

// toggle performs the bridge round trip for one button and returns the new
// state.
func (p *plugin) toggle(ctx context.Context, action string, s Settings) (bool, error) {
	kind, err := kindOf(action)
	if err != nil {
		return false, err
	}
	client, _, err := p.bridges.Connect(ctx, s.Bridge)
	if err != nil {
		return false, err
	}
	if kind == "light" {
		return client.ToggleLight(ctx, s.Target)
	}
	groupedID, err := p.groupedLightID(ctx, client, kind, s)
	if err != nil {
		return false, err
	}
	return client.ToggleGroupedLight(ctx, groupedID)
}

// groupedLightID returns the grouped_light behind a room or zone. Settings
// written by the inspector carry it; older or hand-written settings may not,
// in which case it is looked up.
func (p *plugin) groupedLightID(ctx context.Context, client *hue.Client, kind string, s Settings) (string, error) {
	if s.GroupedLight != "" {
		return s.GroupedLight, nil
	}
	var groups []hue.Group
	var err error
	if kind == "room" {
		groups, err = client.Rooms(ctx)
	} else {
		groups, err = client.Zones(ctx)
	}
	if err != nil {
		return "", err
	}
	for _, g := range groups {
		if g.ID == s.Target {
			if g.GroupedLightID == "" {
				return "", fmt.Errorf("%s %q has no lights", kind, g.Name)
			}
			return g.GroupedLightID, nil
		}
	}
	return "", fmt.Errorf("%s %s no longer exists on the bridge", kind, s.Target)
}

// refreshState reads the target's current state and updates the button, so
// a button shows the truth as soon as it appears. Runs in a goroutine for
// the same reason as onKeyDown.
func (p *plugin) refreshState(ctx context.Context, ev openaction.Event, s Settings) {
	if s.Target == "" {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, bridgeTimeout)
		defer cancel()

		on, err := p.currentState(ctx, ev.Action, s)
		if err != nil {
			p.info.Printf("refresh %s %q failed: %v", s.Kind, s.Name, err)
			return
		}
		if err := p.conn.SetState(ctx, ev.Context, stateIndex(on)); err != nil {
			p.info.Printf("setState for %s failed: %v", ev.Context, err)
		}
	}()
}

func (p *plugin) currentState(ctx context.Context, action string, s Settings) (bool, error) {
	kind, err := kindOf(action)
	if err != nil {
		return false, err
	}
	client, _, err := p.bridges.Connect(ctx, s.Bridge)
	if err != nil {
		return false, err
	}
	if kind == "light" {
		light, err := client.Light(ctx, s.Target)
		if err != nil {
			return false, err
		}
		return light.IsOn(), nil
	}
	groupedID, err := p.groupedLightID(ctx, client, kind, s)
	if err != nil {
		return false, err
	}
	state, err := client.GroupedLight(ctx, groupedID)
	if err != nil {
		return false, err
	}
	return state.On.On, nil
}

func stateIndex(on bool) int {
	if on {
		return stateOn
	}
	return stateOff
}

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}
