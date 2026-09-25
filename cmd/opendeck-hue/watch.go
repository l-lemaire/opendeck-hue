package main

import (
	"context"
	"time"

	"github.com/l-lemaire/opendeck-hue/internal/hue"
	"github.com/l-lemaire/opendeck-hue/internal/openaction"
)

// Live state. The plugin keeps a registry of the buttons currently on the
// deck and one event stream per bridge they use. When the bridge reports a
// light or grouped_light changing, every button showing it is updated, so
// a switch flipped from the Hue app, the CLI or a motion sensor is
// reflected on the deck within a second.

// button is what the registry knows about one key on the deck.
type button struct {
	action   string
	settings Settings
}

// trackButton records a configured button and makes sure its bridge is
// being watched. Called on willAppear and didReceiveSettings.
func (p *plugin) trackButton(ctx context.Context, ev openaction.Event, s Settings) {
	p.mu.Lock()
	if p.buttons == nil {
		p.buttons = map[string]button{}
		p.watchers = map[string]bool{}
	}
	p.buttons[ev.Context] = button{action: ev.Action, settings: s}
	startWatcher := !p.watchers[s.Bridge]
	p.watchers[s.Bridge] = true
	p.mu.Unlock()

	if startWatcher {
		go p.watchBridge(ctx, s.Bridge)
	}
}

// forgetButton drops a button that left the deck (willDisappear). The
// bridge watcher stays; it is cheap and the button may come back.
func (p *plugin) forgetButton(buttonContext string) {
	p.mu.Lock()
	delete(p.buttons, buttonContext)
	p.mu.Unlock()
}

// watchBridge runs for the plugin's lifetime: it opens the event stream and
// reconnects after failures. bridgeKey is the bridge id from the settings
// ("" for the default bridge).
func (p *plugin) watchBridge(ctx context.Context, bridgeKey string) {
	// Connecting can fail (no config yet, keyring locked); retry slowly
	// rather than give up, since the user may fix it while we run.
	var client *hue.Client
	for client == nil {
		c, bridge, err := p.bridges.Connect(ctx, bridgeKey)
		if err == nil {
			client = c
			p.info.Printf("watching bridge %s for changes", bridge.ID)
			break
		}
		p.info.Printf("cannot watch bridge %q yet: %v (retry in 30s)", bridgeKey, err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}

	client.Watch(ctx, hue.EventHandler{
		OnConnect: func() {
			p.info.Printf("event stream connected (bridge %q); refreshing buttons", bridgeKey)
			// Events during an outage were missed: re-read every button.
			p.refreshAll(ctx, bridgeKey)
		},
		OnChange: func(ch hue.Change) {
			if ch.On != nil {
				p.applyChange(ctx, ch)
			}
		},
		OnError: func(err error, retryIn time.Duration) {
			p.info.Printf("event stream lost (bridge %q): %v; retrying in %s", bridgeKey, err, retryIn)
		},
	})
}

// applyChange updates every button that displays the changed resource.
func (p *plugin) applyChange(ctx context.Context, ch hue.Change) {
	p.mu.Lock()
	var affected []string
	for buttonContext, b := range p.buttons {
		s := b.settings
		matches := (ch.ResourceType == "light" && s.Kind != "room" && s.Kind != "zone" && s.Target == ch.ResourceID) ||
			(ch.ResourceType == "grouped_light" && s.GroupedLight == ch.ResourceID)
		if matches {
			affected = append(affected, buttonContext)
		}
	}
	p.mu.Unlock()

	for _, buttonContext := range affected {
		p.info.Printf("%s %s is now %s; updating button %s", ch.ResourceType, ch.ResourceID, onOff(*ch.On), buttonContext)
		if err := p.conn.SetState(ctx, buttonContext, stateIndex(*ch.On)); err != nil {
			p.info.Printf("setState for %s failed: %v", buttonContext, err)
		}
	}
}

// refreshAll re-reads the state of every button on one bridge.
func (p *plugin) refreshAll(ctx context.Context, bridgeKey string) {
	p.mu.Lock()
	var todo []struct {
		ctx string
		b   button
	}
	for buttonContext, b := range p.buttons {
		if b.settings.Bridge == bridgeKey {
			todo = append(todo, struct {
				ctx string
				b   button
			}{buttonContext, b})
		}
	}
	p.mu.Unlock()

	for _, item := range todo {
		p.refreshState(ctx, openaction.Event{Action: item.b.action, Context: item.ctx}, item.b.settings)
	}
}
