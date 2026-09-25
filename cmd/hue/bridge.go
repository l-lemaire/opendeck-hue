package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/l-lemaire/streamdeck/internal/config"
	"github.com/l-lemaire/streamdeck/internal/hue"
	"github.com/l-lemaire/streamdeck/internal/secrets"
)

// targetFlags are the flags every command that talks to a paired bridge
// shares. Registering them from one place keeps the commands consistent.
type targetFlags struct {
	bridge *string
	store  *string
	dryRun *bool
}

// addTargetFlags registers the shared flags on fs. withDryRun is false for
// read-only commands, where the flag would be meaningless.
func addTargetFlags(fs *flag.FlagSet, withDryRun bool) *targetFlags {
	t := &targetFlags{
		bridge: fs.String("bridge", "", "bridge id (default: the default bridge from `hue auth status`)"),
		store:  fs.String("store", secrets.BackendAuto, "credential store: auto, keyring or file"),
	}
	if withDryRun {
		t.dryRun = fs.Bool("dry-run", false, "print the request that would change the bridge instead of sending it")
	} else {
		t.dryRun = new(bool) // always false
	}
	return t
}

// connect loads the bridge configuration and credentials and returns a
// ready client with the pinned certificate. This is the one place that
// assembles a client for a paired bridge.
func (a *app) connect(t *targetFlags) (*hue.Client, config.Bridge, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, config.Bridge{}, err
	}
	b, err := cfg.Resolve(*t.bridge)
	if err != nil {
		return nil, config.Bridge{}, err
	}
	store, err := a.openStore(*t.store)
	if err != nil {
		return nil, config.Bridge{}, err
	}
	creds, err := hue.LoadCredentials(store, b.ID)
	if err != nil {
		return nil, config.Bridge{}, fmt.Errorf("%w (run `hue auth`)", err)
	}

	var dryRun io.Writer // nil means real writes
	if *t.dryRun {
		dryRun = os.Stdout
	}
	client := hue.NewClient(hue.ClientOptions{
		Addr:        b.Addr(),
		ID:          b.ID,
		Fingerprint: b.Fingerprint,
		AppKey:      creds.AppKey,
		Log:         a.log,
		DryRun:      dryRun,
	})
	return client, b, nil
}

// parseArgs parses flags and requires exactly `want` positional words after
// them. It is parseFlags with room for arguments such as a light name.
// Flags must come before the positional words, because the flag package
// stops parsing at the first non-flag word.
func parseArgs(fs *flag.FlagSet, args []string, want int, names ...string) ([]string, error) {
	if err := fs.Parse(args); err != nil {
		return nil, err
	}
	rest := fs.Args()
	switch {
	case len(rest) < want:
		return nil, fmt.Errorf("%s: missing %s", fs.Name(), names[len(rest)])
	case len(rest) > want:
		extra := rest[want]
		hint := ""
		if len(extra) > 0 && extra[0] == '-' {
			hint = " (flags go before the name)"
		}
		return nil, fmt.Errorf("%s: unexpected argument %q%s", fs.Name(), extra, hint)
	}
	return rest, nil
}
