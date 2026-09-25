package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/l-lemaire/opendeck-hue/internal/hue"
)

// discover implements `hue discover`. It looks for bridges with mDNS, falls
// back to the cloud service, and prints what it found.
func (a *app) discover(args []string) error {
	fs := flag.NewFlagSet("hue discover", flag.ContinueOnError)
	asJSON := fs.Bool("json", false, "print the result as JSON")
	timeout := fs.Duration("timeout", hue.DefaultMDNSTimeout, "how long to wait for mDNS answers")
	mdnsOnly := fs.Bool("mdns-only", false, "do not fall back to the cloud discovery service")
	iface := fs.String("interface", "", "network interface to query on (default: let the kernel choose)")
	if err := parseFlags(fs, args); err != nil {
		return err
	}

	d := hue.Discovery{Log: a.log, MDNSTimeout: *timeout, Interface: *iface}

	var (
		bridges []hue.Bridge
		err     error
	)
	if *mdnsOnly {
		bridges, err = d.MDNS(a.ctx)
	} else {
		bridges, err = d.Discover(a.ctx)
	}
	if err != nil {
		return err
	}

	if *asJSON {
		// MarshalIndent produces human-readable JSON; the struct tags on
		// hue.Bridge decide the key names.
		out, err := json.MarshalIndent(bridges, "", "  ")
		if err != nil {
			return err
		}
		fmt.Println(string(out))
		if len(bridges) == 0 {
			return errors.New("no bridge found")
		}
		return nil
	}

	if len(bridges) == 0 {
		return fmt.Errorf("no bridge found after %s (is the bridge on the same network? try --debug)", *timeout)
	}

	// tabwriter aligns columns separated by \t.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tADDRESS\tNAME\tMODEL\tFOUND VIA")
	for _, b := range bridges {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n", b.ID, b.Addr(), b.Name, b.Model, b.Source)
	}
	return w.Flush()
}
