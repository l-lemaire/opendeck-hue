package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/llemaire/streamdeck/internal/hue"
)

// lights dispatches `hue lights <list|on|off|toggle>`.
func (a *app) lights(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("lights: a subcommand is required (list, on, off, toggle)")
	}
	switch args[0] {
	case "list":
		return a.lightsList(args[1:])
	case "on":
		return a.lightsSet(args[1:], "on", true)
	case "off":
		return a.lightsSet(args[1:], "off", false)
	case "toggle":
		return a.lightsToggle(args[1:])
	default:
		return fmt.Errorf("lights: unknown subcommand %q (want: list, on, off, toggle)", args[0])
	}
}

func (a *app) lightsList(args []string) error {
	fs := flag.NewFlagSet("hue lights list", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the result as JSON")
	target := addTargetFlags(fs, false)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	client, _, err := a.connect(target)
	if err != nil {
		return err
	}
	lights, err := client.Lights(a.ctx)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(lights)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tBRIGHT\tTYPE\tID")
	for _, l := range lights {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", l.Name(), onOff(l.IsOn()), brightness(l.Brightness()), l.Metadata.Archetype, l.ID)
	}
	return w.Flush()
}

// lightsSet implements `on` and `off`. `on` also accepts --brightness.
func (a *app) lightsSet(args []string, verb string, on bool) error {
	fs := flag.NewFlagSet("hue lights "+verb, flag.ContinueOnError)
	target := addTargetFlags(fs, true)
	var bright *int
	if on {
		bright = fs.Int("brightness", -1, "brightness in percent, 1 to 100")
	}
	pos, err := parseArgs(fs, args, 1, "light name or id")
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

	client, light, err := a.findLight(target, pos[0])
	if err != nil {
		return err
	}
	if err := client.SetLight(a.ctx, light.ID, update); err != nil {
		return err
	}
	if !*target.dryRun {
		fmt.Printf("%s: %s\n", light.Name(), onOff(on))
	}
	return nil
}

func (a *app) lightsToggle(args []string) error {
	fs := flag.NewFlagSet("hue lights toggle", flag.ContinueOnError)
	target := addTargetFlags(fs, true)
	pos, err := parseArgs(fs, args, 1, "light name or id")
	if err != nil {
		return err
	}
	client, light, err := a.findLight(target, pos[0])
	if err != nil {
		return err
	}
	nowOn, err := client.ToggleLight(a.ctx, light.ID)
	if err != nil {
		return err
	}
	if !*target.dryRun {
		fmt.Printf("%s: %s\n", light.Name(), onOff(nowOn))
	}
	return nil
}

// findLight connects and resolves a name or id against the bridge's lights.
func (a *app) findLight(target *targetFlags, query string) (*hue.Client, hue.Light, error) {
	client, _, err := a.connect(target)
	if err != nil {
		return nil, hue.Light{}, err
	}
	lights, err := client.Lights(a.ctx)
	if err != nil {
		return nil, hue.Light{}, err
	}
	light, err := hue.MatchLight(lights, query)
	if err != nil {
		return nil, hue.Light{}, fmt.Errorf("light: %w (see `hue lights list`)", err)
	}
	return client, light, nil
}

// Small formatting helpers shared by the lights and rooms tables.

func onOff(on bool) string {
	if on {
		return "on"
	}
	return "off"
}

func brightness(pct float64, ok bool) string {
	if !ok {
		return "-"
	}
	return strconv.Itoa(int(pct+0.5)) + "%"
}

func printJSON(v any) error {
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(out))
	return nil
}
