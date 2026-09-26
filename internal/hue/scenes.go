package hue

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"time"
)

// Scenes are the v2 "scene" resource: a named set of per-light settings that
// belongs to one room or zone (its group). Recalling a scene applies those
// settings to the group's lights. The bridge tracks whether a scene is the
// one currently active in its group and reports that in status.active, on
// the event stream too, so a key can show it.
//
//	GET /clip/v2/resource/scene
//	PUT /clip/v2/resource/scene/<id>  {"recall":{"action":"active"}}

// Scene is the resource reduced to what we use.
type Scene struct {
	ID       string `json:"id"`
	Metadata struct {
		Name string `json:"name"`
	} `json:"metadata"`
	// Group is the room or zone the scene belongs to.
	Group  Ref `json:"group"`
	Status struct {
		// Active is "inactive", "static" or "dynamic_palette".
		Active string `json:"active"`
	} `json:"status"`
	// AutoDynamic is set by the Hue app on scenes meant to animate.
	AutoDynamic bool `json:"auto_dynamic"`
}

// Name returns the user-visible name.
func (s Scene) Name() string { return s.Metadata.Name }

// IsActive reports whether the scene is the one currently applied to its
// group, statically or animating.
func (s Scene) IsActive() bool {
	return s.Status.Active != "" && s.Status.Active != "inactive"
}

// Scenes returns every scene, sorted by group id then name so scenes of one
// room stay together.
func (c *Client) Scenes(ctx context.Context) ([]Scene, error) {
	var scenes []Scene
	if err := c.v2(ctx, http.MethodGet, "scene", nil, &scenes); err != nil {
		return nil, err
	}
	sort.Slice(scenes, func(i, j int) bool {
		if scenes[i].Group.RID != scenes[j].Group.RID {
			return scenes[i].Group.RID < scenes[j].Group.RID
		}
		return scenes[i].Name() < scenes[j].Name()
	})
	return scenes, nil
}

// Scene fetches one scene by id.
func (c *Client) Scene(ctx context.Context, id string) (Scene, error) {
	return getOne[Scene](ctx, c, "scene/"+id)
}

// RecallOptions tunes how a scene is applied. The zero value is a plain
// static recall, which works for every scene.
type RecallOptions struct {
	// Dynamic starts the scene's colour animation instead of applying it
	// statically. Only scenes built from a palette animate.
	Dynamic bool
	// Brightness, if set, overrides the scene's brightness (percent).
	Brightness *float64
	// Duration, if set, fades into the scene over this time.
	Duration time.Duration
}

// RecallScene applies a scene to its group.
func (c *Client) RecallScene(ctx context.Context, id string, o RecallOptions) error {
	recall := map[string]any{"action": "active"}
	if o.Dynamic {
		recall["action"] = "dynamic_palette"
	}
	if o.Duration > 0 {
		recall["duration"] = int(o.Duration / time.Millisecond)
	}
	if o.Brightness != nil {
		recall["dimming"] = map[string]float64{"brightness": *o.Brightness}
	}
	return c.v2(ctx, http.MethodPut, "scene/"+id, map[string]any{"recall": recall}, nil)
}

// ScenesOf filters scenes to those belonging to one group.
func ScenesOf(scenes []Scene, groupID string) []Scene {
	var out []Scene
	for _, s := range scenes {
		if s.Group.RID == groupID {
			out = append(out, s)
		}
	}
	return out
}

// MatchScene resolves a scene name or id among the given scenes, which
// should already be limited to one group since names repeat across rooms.
func MatchScene(scenes []Scene, query string) (Scene, error) {
	s, err := Match(scenes, query, func(s Scene) string { return s.ID }, Scene.Name)
	if err != nil {
		return Scene{}, fmt.Errorf("scene: %w", err)
	}
	return s, nil
}
