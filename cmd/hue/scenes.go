package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/l-lemaire/opendeck-hue/internal/hue"
)

// Scenes belong to a room or a zone, and their names repeat across rooms
// ("Relax" exists in every room the Hue app set up). So a scene is always
// named together with its group:
//
//	hue list scenes [--in <room or zone>]
//	hue scene <room or zone> <scene> [--dynamic] [--dry-run]

// listScenes implements `hue list scenes`.
func (a *app) listScenes(args []string) error {
	fs := flag.NewFlagSet("hue list scenes", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the result as JSON")
	in := fs.String("in", "", "only scenes of this room or zone (name or id)")
	target := addTargetFlags(fs, false)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	client, _, err := a.connect(target)
	if err != nil {
		return err
	}
	groups, err := client.Groups(a.ctx)
	if err != nil {
		return err
	}
	scenes, err := client.Scenes(a.ctx)
	if err != nil {
		return err
	}
	if *in != "" {
		g, err := hue.MatchGroup(groups, *in)
		if err != nil {
			return fmt.Errorf("room or zone: %w (see `hue list rooms`)", err)
		}
		scenes = hue.ScenesOf(scenes, g.ID)
	}

	// Rows carry the group's name so the output is readable and the JSON is
	// self-contained.
	groupByID := map[string]hue.Group{}
	for _, g := range groups {
		groupByID[g.ID] = g
	}
	type row struct {
		Scene  string `json:"scene"`
		In     string `json:"in"`
		Kind   string `json:"kind"`
		Status string `json:"status"`
		ID     string `json:"id"`
	}
	var rows []row
	for _, s := range scenes {
		g := groupByID[s.Group.RID]
		rows = append(rows, row{Scene: s.Name(), In: g.Name, Kind: g.Kind, Status: s.Status.Active, ID: s.ID})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].In != rows[j].In {
			return rows[i].In < rows[j].In
		}
		return rows[i].Scene < rows[j].Scene
	})
	if *asJSON {
		return printJSON(rows)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SCENE\tIN\tKIND\tSTATUS\tID")
	for _, r := range rows {
		status := r.Status
		if status == "inactive" {
			status = "-"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", r.Scene, r.In, r.Kind, status, r.ID)
	}
	return w.Flush()
}

// scene implements `hue scene <group> <scene>`: recall a scene.
func (a *app) scene(args []string) error {
	fs := flag.NewFlagSet("hue scene", flag.ContinueOnError)
	dynamic := fs.Bool("dynamic", false, "start the scene's colour animation instead of applying it statically")
	brightness := fs.Int("brightness", -1, "override the scene's brightness, 1 to 100")
	target := addTargetFlags(fs, true)
	pos, err := parseArgs(fs, args, 2, "room or zone name", "scene name")
	if err != nil {
		return err
	}
	opts := hue.RecallOptions{Dynamic: *dynamic}
	if *brightness != -1 {
		if *brightness < 1 || *brightness > 100 {
			return fmt.Errorf("brightness %d is out of range 1 to 100", *brightness)
		}
		opts.Brightness = hue.Float(float64(*brightness))
	}

	client, _, err := a.connect(target)
	if err != nil {
		return err
	}
	groups, err := client.Groups(a.ctx)
	if err != nil {
		return err
	}
	g, err := hue.MatchGroup(groups, pos[0])
	if err != nil {
		return fmt.Errorf("room or zone: %w (see `hue list rooms`)", err)
	}
	scenes, err := client.Scenes(a.ctx)
	if err != nil {
		return err
	}
	candidates := hue.ScenesOf(scenes, g.ID)
	if len(candidates) == 0 {
		return fmt.Errorf("%s %q has no scenes", g.Kind, g.Name)
	}
	s, err := hue.MatchScene(candidates, pos[1])
	if err != nil {
		return fmt.Errorf("%w (see `hue list scenes --in %q`)", err, g.Name)
	}
	if err := client.RecallScene(a.ctx, s.ID, opts); err != nil {
		return err
	}
	if !*target.dryRun {
		mode := "applied"
		if *dynamic {
			mode = "animating"
		}
		fmt.Printf("scene %s in %s %s: %s\n", s.Name(), g.Kind, g.Name, mode)
	}
	return nil
}
