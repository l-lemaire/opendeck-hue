package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// actionPrefix is shared by the three action UUIDs in plugin/manifest.json.
const actionPrefix = "com.github.llemaire.hue."

// Settings is what a button remembers. The property inspector writes it
// with setSettings; OpenDeck stores it in the profile and hands it back in
// every event about the button. Nothing here is secret.
type Settings struct {
	// Bridge is the bridge id; empty means the default bridge.
	Bridge string `json:"bridge,omitempty"`
	// Kind is "light", "room" or "zone". Redundant with the action UUID but
	// handy when reading a profile by hand.
	Kind string `json:"kind,omitempty"`
	// Target is the v2 id of the light, room or zone.
	Target string `json:"target,omitempty"`
	// GroupedLight is the grouped_light id for rooms and zones, so a key
	// press needs one request less. Empty for lights.
	GroupedLight string `json:"grouped_light,omitempty"`
	// Name is the target's name at the time it was chosen, for the title.
	Name string `json:"name,omitempty"`

	// Label controls the text the plugin writes on the key:
	//   ""  or "name"  the target's name (default)
	//   "custom"       the text in CustomLabel
	//   "none"         nothing from the plugin; OpenDeck's own title
	//                  settings for the key apply
	Label       string `json:"label,omitempty"`
	CustomLabel string `json:"custom_label,omitempty"`
}

// title returns the text the plugin should put on the key, and whether it
// should touch the title at all.
func (s Settings) title() (text string, set bool) {
	switch s.Label {
	case "none":
		return "", true // clear what we may have written earlier
	case "custom":
		return s.CustomLabel, true
	default:
		if s.Name == "" {
			return "", false
		}
		return s.Name, true
	}
}

// decodeSettings turns the raw JSON from an event into Settings. Empty or
// absent settings (a freshly placed button) decode to the zero value.
func decodeSettings(raw json.RawMessage) (Settings, error) {
	var s Settings
	if len(raw) == 0 || string(raw) == "null" {
		return s, nil
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return s, fmt.Errorf("decode settings: %w", err)
	}
	return s, nil
}

// kindOf maps an action UUID to the entity kind it controls:
// "com.github.llemaire.hue.toggle-light" -> "light".
func kindOf(actionUUID string) (string, error) {
	name := strings.TrimPrefix(actionUUID, actionPrefix)
	switch name {
	case "toggle-light":
		return "light", nil
	case "toggle-room":
		return "room", nil
	case "toggle-zone":
		return "zone", nil
	default:
		return "", fmt.Errorf("unknown action %q", actionUUID)
	}
}

// shortAction strips the plugin prefix for log lines.
func shortAction(uuid string) string {
	return strings.TrimPrefix(uuid, actionPrefix)
}
