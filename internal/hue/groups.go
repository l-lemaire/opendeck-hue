package hue

import (
	"context"
	"fmt"
	"net/http"
	"sort"
)

// Rooms and zones group lights. Both have the same shape in the API; a room
// contains devices, a zone contains lights, and a light may be in several
// zones. Each one owns a "grouped_light" service, which is the thing you
// actually switch on and off. The plugin will map Stream Deck buttons to
// these, so the client exposes them as one flattened Group type.

// Group is a room or a zone together with its grouped_light state.
type Group struct {
	ID   string // id of the room/zone resource
	Name string
	Kind string // "room" or "zone"
	// GroupedLightID is the id to use for on/off; empty if the group has
	// no lights yet.
	GroupedLightID string
	Members        int // devices (room) or lights (zone)
	On             bool
	Brightness     *float64 // nil when unknown or not dimmable
}

// rawGroup is the wire shape shared by the room and zone resources.
type rawGroup struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	Children []Ref `json:"children"`
	Services []Ref `json:"services"`
}

// GroupedLight is the v2 "grouped_light" resource.
type GroupedLight struct {
	ID    string `json:"id"`
	Owner Ref    `json:"owner"` // the room or zone
	On    struct {
		On bool `json:"on"`
	} `json:"on"`
	Dimming *struct {
		Brightness float64 `json:"brightness"`
	} `json:"dimming,omitempty"`
}

// Rooms lists the rooms with their current state, sorted by name.
func (c *Client) Rooms(ctx context.Context) ([]Group, error) {
	return c.groups(ctx, "room")
}

// Zones lists the zones with their current state, sorted by name.
func (c *Client) Zones(ctx context.Context) ([]Group, error) {
	return c.groups(ctx, "zone")
}

// Groups lists rooms and zones together. Group.Kind tells them apart.
func (c *Client) Groups(ctx context.Context) ([]Group, error) {
	return c.groups(ctx, "room", "zone")
}

// groups fetches the given kinds ("room", "zone") plus grouped_light, and
// joins them in memory: one GET per kind and one for the states.
func (c *Client) groups(ctx context.Context, kinds ...string) ([]Group, error) {
	var raws []rawGroup
	for _, kind := range kinds {
		var batch []rawGroup
		if err := c.v2(ctx, http.MethodGet, kind, nil, &batch); err != nil {
			return nil, err
		}
		raws = append(raws, batch...)
	}
	var grouped []GroupedLight
	if err := c.v2(ctx, http.MethodGet, "grouped_light", nil, &grouped); err != nil {
		return nil, err
	}
	stateByID := make(map[string]GroupedLight, len(grouped))
	for _, g := range grouped {
		stateByID[g.ID] = g
	}

	groups := make([]Group, 0, len(raws))
	for _, raw := range raws {
		g := Group{ID: raw.ID, Name: raw.Metadata.Name, Kind: raw.Type, Members: len(raw.Children)}
		for _, s := range raw.Services {
			if s.RType == "grouped_light" {
				g.GroupedLightID = s.RID
			}
		}
		if state, ok := stateByID[g.GroupedLightID]; ok {
			g.On = state.On.On
			if state.Dimming != nil {
				g.Brightness = &state.Dimming.Brightness
			}
		}
		groups = append(groups, g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups, nil
}

// GroupedLight fetches the current state of one grouped_light.
func (c *Client) GroupedLight(ctx context.Context, id string) (GroupedLight, error) {
	return getOne[GroupedLight](ctx, c, "grouped_light/"+id)
}

// SetGroup applies an update to a room or zone through its grouped_light.
// Note the bridge rate-limits grouped_light writes to about one per second.
func (c *Client) SetGroup(ctx context.Context, g Group, u Update) error {
	if g.GroupedLightID == "" {
		return fmt.Errorf("%s %q has no lights", g.Kind, g.Name)
	}
	return c.v2(ctx, http.MethodPut, "grouped_light/"+g.GroupedLightID, u.body(), nil)
}

// ToggleGroup flips a room or zone and returns the new state. A group is
// considered "on" when any of its lights is on, which is what the bridge
// reports in grouped_light.on.
func (c *Client) ToggleGroup(ctx context.Context, g Group) (nowOn bool, err error) {
	if g.GroupedLightID == "" {
		return false, fmt.Errorf("%s %q has no lights", g.Kind, g.Name)
	}
	return c.ToggleGroupedLight(ctx, g.GroupedLightID)
}

// ToggleGroupedLight flips a grouped_light by id. Callers that already know
// the id (the plugin stores it in the button settings) skip the room/zone
// lookup that ToggleGroup needs.
func (c *Client) ToggleGroupedLight(ctx context.Context, groupedLightID string) (nowOn bool, err error) {
	state, err := c.GroupedLight(ctx, groupedLightID)
	if err != nil {
		return false, err
	}
	nowOn = !state.On.On
	return nowOn, c.v2(ctx, http.MethodPut, "grouped_light/"+groupedLightID, Update{On: &nowOn}.body(), nil)
}
