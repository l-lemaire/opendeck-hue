package hue

import (
	"context"
	"net/http"
	"sort"
)

// Light is the v2 "light" resource, reduced to the fields we use. The JSON
// has many more (color, effects, signaling...). Unknown fields are ignored
// by encoding/json, so this struct stays small on purpose.
type Light struct {
	ID   string `json:"id"`
	IDV1 string `json:"id_v1,omitempty"` // legacy path like "/lights/3"; handy to recognise a bulb
	// Owner points at the device (the physical bulb) this light belongs to.
	Owner    Ref `json:"owner"`
	Metadata struct {
		Name      string `json:"name"`
		Archetype string `json:"archetype"` // e.g. "sultan_bulb", "plug", "spot_bulb"
	} `json:"metadata"`
	On struct {
		On bool `json:"on"`
	} `json:"on"`
	// Dimming is a pointer because plugs and some devices have none, and
	// encoding/json leaves a pointer nil when the key is absent.
	Dimming *struct {
		Brightness float64 `json:"brightness"` // 0.0 to 100.0
	} `json:"dimming,omitempty"`
}

// Name returns the user-visible name.
func (l Light) Name() string { return l.Metadata.Name }

// IsOn reports whether the light is on.
func (l Light) IsOn() bool { return l.On.On }

// Brightness returns the level in percent and whether the light can dim.
func (l Light) Brightness() (float64, bool) {
	if l.Dimming == nil {
		return 0, false
	}
	return l.Dimming.Brightness, true
}

// Lights returns every light known to the bridge, sorted by name.
func (c *Client) Lights(ctx context.Context) ([]Light, error) {
	var lights []Light
	if err := c.v2(ctx, http.MethodGet, "light", nil, &lights); err != nil {
		return nil, err
	}
	sort.Slice(lights, func(i, j int) bool { return lights[i].Name() < lights[j].Name() })
	return lights, nil
}

// Light fetches one light by its v2 id.
func (c *Client) Light(ctx context.Context, id string) (Light, error) {
	return getOne[Light](ctx, c, "light/"+id)
}

// Update describes a change to a light or a group. Nil fields are left
// untouched on the bridge; that is why the fields are pointers rather than
// plain values, so "false" and "not specified" can be told apart.
type Update struct {
	On         *bool
	Brightness *float64 // percent, 0 to 100; implies on
}

// body converts the update into the partial JSON object the API expects.
func (u Update) body() map[string]any {
	out := map[string]any{}
	if u.On != nil {
		out["on"] = map[string]bool{"on": *u.On}
	}
	if u.Brightness != nil {
		out["dimming"] = map[string]float64{"brightness": *u.Brightness}
	}
	return out
}

// SetLight applies an update to one light.
func (c *Client) SetLight(ctx context.Context, id string, u Update) error {
	return c.v2(ctx, http.MethodPut, "light/"+id, u.body(), nil)
}

// ToggleLight reads the current state and flips it. It returns the new
// state. Two requests are needed: the API has no atomic toggle.
func (c *Client) ToggleLight(ctx context.Context, id string) (nowOn bool, err error) {
	light, err := c.Light(ctx, id)
	if err != nil {
		return false, err
	}
	nowOn = !light.IsOn()
	return nowOn, c.SetLight(ctx, id, Update{On: &nowOn})
}

// Bool and Float are tiny helpers to build an Update inline:
// Update{On: hue.Bool(true)}. Go cannot take the address of a literal.
func Bool(b bool) *bool        { return &b }
func Float(f float64) *float64 { return &f }
