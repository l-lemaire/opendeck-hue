package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/l-lemaire/opendeck-hue/internal/config"
	"github.com/l-lemaire/opendeck-hue/internal/hue"
	"github.com/l-lemaire/opendeck-hue/internal/pairing"
	"github.com/l-lemaire/opendeck-hue/internal/secrets"
)

// auth dispatches `hue auth`, `hue auth status` and `hue auth forget`.
// A first word that is neither a known subcommand nor a flag is an error,
// never silently treated as "pair".
func (a *app) auth(args []string) error {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "status":
			return a.authStatus(args[1:])
		case "forget":
			return a.authForget(args[1:])
		default:
			return fmt.Errorf("auth: unknown subcommand %q (want: status, forget, or flags for pairing)", args[0])
		}
	}
	return a.authPair(args)
}

// openStore opens the credential store with the file fallback located next
// to the config file, and warns when the weaker file backend ends up in use
// without being asked for.
func (a *app) openStore(backend string) (secrets.Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	store, err := secrets.Open(backend, filepath.Join(dir, "credentials.json"), a.log)
	if err != nil {
		return nil, err
	}
	if backend != secrets.BackendFile && strings.HasPrefix(store.Name(), "file") {
		fmt.Fprintln(os.Stderr, "warning: no desktop keyring reachable, storing credentials in", store.Name())
	}
	return store, nil
}

// authPair implements `hue auth`: the shared pairing flow with progress
// printed to the terminal.
func (a *app) authPair(args []string) error {
	fs := flag.NewFlagSet("hue auth", flag.ContinueOnError)
	ip := fs.String("ip", "", "bridge address (host or host:port) instead of discovery")
	id := fs.String("id", "", "bridge id to pair with when several are found")
	device := fs.String("name", "", "device name recorded on the bridge (max 19 chars; default: host name)")
	backend := fs.String("store", secrets.BackendAuto, "where to keep the key: auto, keyring or file")
	timeout := fs.Duration("timeout", pairing.DefaultTimeout, "how long to wait for the link button")
	force := fs.Bool("force", false, "pair again even if this bridge already has a key")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	store, err := a.openStore(*backend)
	if err != nil {
		return err
	}

	waiting := false
	bridge, err := pairing.Run(a.ctx, pairing.Options{
		Addr: *ip, ID: *id, DeviceName: *device, Store: store, Timeout: *timeout, Force: *force, Log: a.log,
		Report: func(p pairing.Progress) {
			switch p.Stage {
			case pairing.StageWaiting:
				if !waiting {
					fmt.Printf("%s. Waiting up to %ds", p.Message, p.SecondsLeft)
					waiting = true
				} else {
					fmt.Print(".")
				}
			default:
				if waiting {
					fmt.Println()
					waiting = false
				}
				fmt.Println(p.Message)
			}
		},
	})
	if waiting {
		fmt.Println()
	}
	if err != nil {
		if errors.Is(err, pairing.ErrAlreadyPaired) {
			return fmt.Errorf("%v; use --force to pair again", err)
		}
		return err
	}
	cfgPath, _ := config.Dir()
	fmt.Printf("Bridge details and certificate fingerprint saved in %s.\n", filepath.Join(cfgPath, "config.json"))
	_ = bridge
	return nil
}

// authStatus implements `hue auth status`: lists known bridges and checks
// that their keys still work.
func (a *app) authStatus(args []string) error {
	fs := flag.NewFlagSet("hue auth status", flag.ContinueOnError)
	backend := fs.String("store", secrets.BackendAuto, "credential store to read: auto, keyring or file")
	offline := fs.Bool("offline", false, "do not contact the bridges")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if len(cfg.Bridges) == 0 {
		fmt.Println("No bridge paired. Run `hue auth`.")
		return nil
	}
	store, err := a.openStore(*backend)
	if err != nil {
		return err
	}
	fmt.Println("Credential store:", store.Name())
	fmt.Println("Config file:     ", cfg.Path())
	fmt.Println()

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tADDRESS\tNAME\tDEFAULT\tKEY\tSTATUS")
	for _, b := range cfg.Bridges {
		isDefault := ""
		if b.ID == cfg.DefaultBridge {
			isDefault = "*"
		}
		keyState, status := "present", "not checked"
		creds, err := hue.LoadCredentials(store, b.ID)
		switch {
		case errors.Is(err, secrets.ErrNotFound):
			keyState, status = "missing", "run `hue auth --force`"
		case err != nil:
			keyState, status = "error", err.Error()
		case !*offline:
			client := hue.NewClient(hue.ClientOptions{Addr: b.Addr(), ID: b.ID, Fingerprint: b.Fingerprint, AppKey: creds.AppKey, Log: a.log})
			if err := client.CheckKey(a.ctx); err != nil {
				status = err.Error()
			} else {
				status = "ok"
			}
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n", b.ID, b.Addr(), b.Name, isDefault, keyState, status)
	}
	return w.Flush()
}

// authForget implements `hue auth forget`: removes the key from the store
// and the bridge from the config. The key itself stays valid on the bridge;
// it can be revoked in the Hue app under Settings > Apps.
func (a *app) authForget(args []string) error {
	fs := flag.NewFlagSet("hue auth forget", flag.ContinueOnError)
	id := fs.String("id", "", "bridge id (default: the default bridge)")
	backend := fs.String("store", secrets.BackendAuto, "credential store: auto, keyring or file")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	if err := parseFlags(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	b, err := cfg.Resolve(*id)
	if err != nil {
		return err
	}
	store, err := a.openStore(*backend)
	if err != nil {
		return err
	}

	if !*yes {
		fmt.Printf("Forget bridge %s (%s) and delete its key from the %s? [y/N] ", b.ID, b.Name, store.Name())
		answer, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		if strings.ToLower(strings.TrimSpace(answer)) != "y" {
			return errors.New("aborted")
		}
	}

	if err := store.Delete(b.ID); err != nil && !errors.Is(err, secrets.ErrNotFound) {
		return err
	}
	cfg.Remove(b.ID)
	if err := cfg.Save(); err != nil {
		return err
	}
	fmt.Printf("Forgot bridge %s. The key still exists on the bridge; revoke it in the Hue app if needed.\n", b.ID)
	return nil
}
