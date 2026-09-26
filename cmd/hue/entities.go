package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"text/tabwriter"

	"github.com/l-lemaire/opendeck-hue/internal/hue"
)

// The control commands are verb first, then the kind of thing:
//
//	hue list   lights|rooms|zones
//	hue on     light|room|zone <name or id>
//	hue off    light|room|zone <name or id>
//	hue toggle light|room|zone <name or id>
//
// Lights, rooms and zones are separate namespaces: a name is only matched
// against entities of the kind given on the command line.

// kind identifies one of the three entity types. Both the singular and the
// plural spelling are accepted on the command line.
type kind string

const (
	kindLight kind = "light"
	kindRoom  kind = "room"
	kindZone  kind = "zone"
)

func parseKind(word string) (kind, error) {
	switch word {
	case "light", "lights":
		return kindLight, nil
	case "room", "rooms":
		return kindRoom, nil
	case "zone", "zones":
		return kindZone, nil
	default:
		return "", fmt.Errorf("unknown kind %q (want: light, room or zone)", word)
	}
}

// list implements `hue list <kind>`.
func (a *app) list(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("list: what to list is required (lights, rooms, zones or scenes)")
	}
	if args[0] == "scene" || args[0] == "scenes" {
		return a.listScenes(args[1:])
	}
	k, err := parseKind(args[0])
	if err != nil {
		return fmt.Errorf("list: %w", err)
	}
	fs := flag.NewFlagSet("hue list "+args[0], flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the result as JSON")
	target := addTargetFlags(fs, false)
	if err := parseFlags(fs, args[1:]); err != nil {
		return err
	}
	client, _, err := a.connect(target)
	if err != nil {
		return err
	}

	if k == kindLight {
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

	groups, err := fetchGroups(a, client, k)
	if err != nil {
		return err
	}
	if *asJSON {
		return printJSON(groups)
	}
	members := "DEVICES" // rooms contain devices ...
	if k == kindZone {
		members = "LIGHTS" // ... zones contain lights
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "NAME\tSTATE\tBRIGHT\t%s\tID\n", members)
	for _, g := range groups {
		b, ok := 0.0, false
		if g.Brightness != nil {
			b, ok = *g.Brightness, true
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%s\n", g.Name, onOff(g.On), brightness(b, ok), g.Members, g.ID)
	}
	return w.Flush()
}

// power implements `hue on|off|toggle <kind> <name or id>`. verb is one of
// "on", "off", "toggle".
func (a *app) power(verb string, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("%s: what to switch is required (light, room or zone)", verb)
	}
	k, err := parseKind(args[0])
	if err != nil {
		return fmt.Errorf("%s: %w", verb, err)
	}
	fs := flag.NewFlagSet("hue "+verb+" "+string(k), flag.ContinueOnError)
	target := addTargetFlags(fs, true)
	var bright *int
	if verb == "on" {
		bright = fs.Int("brightness", -1, "brightness in percent, 1 to 100")
	}
	pos, err := parseArgs(fs, args[1:], 1, string(k)+" name or id")
	if err != nil {
		return err
	}

	// Build the update for on/off. toggle decides after reading the state.
	update := hue.Update{}
	switch verb {
	case "on":
		update.On = hue.Bool(true)
		if *bright != -1 {
			if *bright < 1 || *bright > 100 {
				return fmt.Errorf("brightness %d is out of range 1 to 100", *bright)
			}
			update.Brightness = hue.Float(float64(*bright))
		}
	case "off":
		update.On = hue.Bool(false)
	}

	client, _, err := a.connect(target)
	if err != nil {
		return err
	}

	var name string
	var nowOn bool
	if k == kindLight {
		lights, err := client.Lights(a.ctx)
		if err != nil {
			return err
		}
		light, err := hue.MatchLight(lights, pos[0])
		if err != nil {
			return fmt.Errorf("light: %w (see `hue list lights`)", err)
		}
		name = light.Name()
		if verb == "toggle" {
			nowOn, err = client.ToggleLight(a.ctx, light.ID)
		} else {
			nowOn, err = *update.On, client.SetLight(a.ctx, light.ID, update)
		}
		if err != nil {
			return err
		}
	} else {
		groups, err := fetchGroups(a, client, k)
		if err != nil {
			return err
		}
		group, err := hue.MatchGroup(groups, pos[0])
		if err != nil {
			return fmt.Errorf("%s: %w (see `hue list %ss`)", k, err, k)
		}
		name = group.Name
		if verb == "toggle" {
			nowOn, err = client.ToggleGroup(a.ctx, group)
		} else {
			nowOn, err = *update.On, client.SetGroup(a.ctx, group, update)
		}
		if err != nil {
			return err
		}
	}

	if !*target.dryRun {
		fmt.Printf("%s %s: %s\n", k, name, onOff(nowOn))
	}
	return nil
}

// fetchGroups returns rooms or zones depending on the kind.
func fetchGroups(a *app, client *hue.Client, k kind) ([]hue.Group, error) {
	if k == kindZone {
		return client.Zones(a.ctx)
	}
	return client.Rooms(a.ctx)
}

// Small formatting helpers.

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
