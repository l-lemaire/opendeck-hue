package hue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/l-lemaire/opendeck-hue/internal/hue/huetest"
)

func TestScenesListAndRecall(t *testing.T) {
	fb := huetest.New(t, testBridgeID)
	c := pairedClient(t, fb)
	ctx := context.Background()

	scenes, err := c.Scenes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(scenes) != 3 {
		t.Fatalf("got %d scenes, want 3", len(scenes))
	}
	kitchen := ScenesOf(scenes, huetest.RoomKitchen)
	if len(kitchen) != 2 || kitchen[0].Name() != "Bright" || kitchen[1].Name() != "Relax" || kitchen[0].Group.RType != "room" {
		t.Errorf("kitchen scenes = %+v", kitchen)
	}
	for _, s := range scenes {
		if s.IsActive() {
			t.Errorf("%s should start inactive", s.Name())
		}
	}

	relax, err := MatchScene(kitchen, "rel")
	if err != nil || relax.ID != huetest.SceneRelax {
		t.Fatalf("match = %+v, %v", relax, err)
	}
	if err := c.RecallScene(ctx, relax.ID, RecallOptions{}); err != nil {
		t.Fatal(err)
	}
	got, _ := c.Scene(ctx, relax.ID)
	if !got.IsActive() || got.Status.Active != "static" {
		t.Errorf("after recall: %+v", got.Status)
	}
	// Recalling another scene of the same group deactivates the first.
	if err := c.RecallScene(ctx, huetest.SceneBright, RecallOptions{Dynamic: true, Brightness: Float(40), Duration: 2 * time.Second}); err != nil {
		t.Fatal(err)
	}
	relaxAgain, _ := c.Scene(ctx, relax.ID)
	bright, _ := c.Scene(ctx, huetest.SceneBright)
	if relaxAgain.IsActive() || bright.Status.Active != "dynamic_palette" {
		t.Errorf("relax=%s bright=%s", relaxAgain.Status.Active, bright.Status.Active)
	}
	if puts := fb.Puts(); len(puts) != 2 || puts[0] != "scene/"+huetest.SceneRelax {
		t.Errorf("puts = %v", puts)
	}
}

func TestSceneStatusOnStream(t *testing.T) {
	fb := huetest.New(t, testBridgeID)
	c := pairedClient(t, fb)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	connected := make(chan struct{})
	changes := make(chan Change, 16)
	go c.StreamEvents(ctx, EventHandler{
		OnConnect: func() { close(connected) },
		OnChange:  func(ch Change) { changes <- ch },
	})
	<-connected

	if err := c.RecallScene(ctx, huetest.SceneRelax, RecallOptions{}); err != nil {
		t.Fatal(err)
	}
	fb.DeactivateScenes(huetest.RoomKitchen) // someone changed a kitchen light by hand

	want := []struct {
		id, status string
		active     bool
	}{
		{huetest.SceneRelax, "static", true},
		{huetest.SceneRelax, "inactive", false},
	}
	for i, w := range want {
		select {
		case got := <-changes:
			if got.ResourceType != "scene" || got.ResourceID != w.id || got.SceneStatus != w.status || got.SceneActive() != w.active {
				t.Errorf("change %d = %+v, want %+v", i, got, w)
			}
		case <-time.After(3 * time.Second):
			t.Fatalf("change %d never arrived", i)
		}
	}
}

func TestRecallSceneDryRun(t *testing.T) {
	fb := huetest.New(t, testBridgeID)
	var out strings.Builder
	c := NewClient(ClientOptions{Addr: fb.Addr(), ID: fb.ID, AppKey: fb.AppKey, DryRun: &out})
	if err := c.RecallScene(context.Background(), huetest.SceneRelax, RecallOptions{Dynamic: true}); err != nil {
		t.Fatal(err)
	}
	if len(fb.Puts()) != 0 || !strings.Contains(out.String(), `"action": "dynamic_palette"`) {
		t.Errorf("dry run wrote to the bridge or printed nothing useful: %q", out.String())
	}
}
