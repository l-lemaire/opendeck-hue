package main

import (
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/llemaire/streamdeck/internal/hue"
)

// rooms dispatches `hue rooms <list|on|off|toggle>`. "Rooms" here covers
// both Hue rooms and zones; the table shows which is which.
func (a *app) rooms(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("rooms: a subcommand is required (list, on, off, toggle)")
	}
	switch args[0] {
	case "list":
		return a.roomsList(args[1:])
	case "on":
		return a.roomsSet(args[1:], "on", true)
	case "off":
		return a.roomsSet(args[1:], "off", false)
	case "toggle":
		return a.roomsToggle(args[1:])
	default:
		return fmt.Errorf("rooms: unknown subcommand %q (want: list, on, off, toggle)", args[0])
	}
}

func (a *app) roomsList(args []string) error {
	fs := flag.NewFlagSet("hue rooms list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the result as JSON")
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
	if *asJSON {
		return printJSON(groups)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tKIND\tSTATE\tBRIGHT\tMEMBERS\tID")
	for _, g := range groups {
		b, ok := 0.0, false
		if g.Brightness != nil {
			b, ok = *g.Brightness, true
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%d\t%s\n", g.Name, g.Kind, onOff(g.On), brightness(b, ok), g.Members, g.ID)
	}
	return w.Flush()
}

func (a *app) roomsSet(args []string, verb string, on bool) error {
	fs := flag.NewFlagSet("hue rooms "+verb, flag.ContinueOnError)
	target := addTargetFlags(fs, true)
	var bright *int
	if on {
		bright = fs.Int("brightness", -1, "brightness in percent, 1 to 100")
	}
	pos, err := parseArgs(fs, args, 1, "room name or id")
	if err != nil {
		return err
	}
	update := hue.Update{On: hue.Bool(on)}
	if bright != nil && *bright != -1 {
		if *bright < 1 || *bright > 100 {
			return fmt.Errorf("brightness %d is out of range 1 to 100", *bright)
		}
		update.Brightness = hue.Float(float64(*bright))
	}

	client, group, err := a.findGroup(target, pos[0])
	if err != nil {
		return err
	}
	if err := client.SetGroup(a.ctx, group, update); err != nil {
		return err
	}
	if !*target.dryRun {
		fmt.Printf("%s: %s\n", group.Name, onOff(on))
	}
	return nil
}

func (a *app) roomsToggle(args []string) error {
	fs := flag.NewFlagSet("hue rooms toggle", flag.ContinueOnError)
	target := addTargetFlags(fs, true)
	pos, err := parseArgs(fs, args, 1, "room name or id")
	if err != nil {
		return err
	}
	client, group, err := a.findGroup(target, pos[0])
	if err != nil {
		return err
	}
	nowOn, err := client.ToggleGroup(a.ctx, group)
	if err != nil {
		return err
	}
	if !*target.dryRun {
		fmt.Printf("%s: %s\n", group.Name, onOff(nowOn))
	}
	return nil
}

func (a *app) findGroup(target *targetFlags, query string) (*hue.Client, hue.Group, error) {
	client, _, err := a.connect(target)
	if err != nil {
		return nil, hue.Group{}, err
	}
	groups, err := client.Groups(a.ctx)
	if err != nil {
		return nil, hue.Group{}, err
	}
	group, err := hue.MatchGroup(groups, query)
	if err != nil {
		return nil, hue.Group{}, fmt.Errorf("room: %w (see `hue rooms list`)", err)
	}
	return client, group, nil
}
