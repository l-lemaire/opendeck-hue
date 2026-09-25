package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/llemaire/streamdeck/internal/hue"
)

// watch implements `hue watch`: print every change the bridge reports until
// Ctrl-C. Useful to see what the plugin sees.
func (a *app) watch(args []string) error {
	fs := flag.NewFlagSet("hue watch", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print one JSON object per change")
	target := addTargetFlags(fs, false)
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	client, bridge, err := a.connect(target)
	if err != nil {
		return err
	}

	// Names for the ids the stream will mention.
	names := map[string]string{}
	lights, err := client.Lights(a.ctx)
	if err != nil {
		return err
	}
	for _, l := range lights {
		names[l.ID] = fmt.Sprintf("light %q", l.Name())
	}
	groups, err := client.Groups(a.ctx)
	if err != nil {
		return err
	}
	for _, g := range groups {
		names[g.GroupedLightID] = fmt.Sprintf("%s %q", g.Kind, g.Name)
	}

	fmt.Fprintf(os.Stderr, "Watching bridge %s; Ctrl-C to stop.\n", bridge.ID)
	client.Watch(a.ctx, hue.EventHandler{
		OnConnect: func() { fmt.Fprintln(os.Stderr, "connected to the event stream") },
		OnError: func(err error, retryIn time.Duration) {
			fmt.Fprintf(os.Stderr, "stream lost: %v; retrying in %s\n", err, retryIn)
		},
		OnChange: func(ch hue.Change) {
			if *asJSON {
				out, _ := json.Marshal(ch)
				fmt.Println(string(out))
				return
			}
			if ch.On == nil && ch.Brightness == nil {
				return // colour, effects and other fields we do not model
			}
			name, ok := names[ch.ResourceID]
			if !ok {
				name = ch.ResourceType + " " + ch.ResourceID
			}
			line := time.Now().Format("15:04:05") + " " + name + ":"
			if ch.On != nil {
				line += " " + onOff(*ch.On)
			}
			if ch.Brightness != nil {
				line += fmt.Sprintf(" %d%%", int(*ch.Brightness+0.5))
			}
			fmt.Println(line)
		},
	})
	return nil
}
