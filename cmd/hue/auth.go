package main

import (
	"bufio"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/llemaire/streamdeck/internal/config"
	"github.com/llemaire/streamdeck/internal/hue"
	"github.com/llemaire/streamdeck/internal/secrets"
)

// appName is the first half of the "devicetype" the bridge records for our
// key. It shows up in the Hue app under Settings > Apps.
const appName = "hue-cli"

// auth dispatches `hue auth`, `hue auth status` and `hue auth forget`.
func (a *app) auth(args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "status":
			return a.authStatus(args[1:])
		case "forget":
			return a.authForget(args[1:])
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

// authPair implements `hue auth`: find the bridge, verify its certificate,
// wait for the link button, save the key.
func (a *app) authPair(args []string) error {
	fs := flag.NewFlagSet("hue auth", flag.ContinueOnError)
	ip := fs.String("ip", "", "bridge address (host or host:port) instead of discovery")
	id := fs.String("id", "", "bridge id to pair with when several are found")
	device := fs.String("name", defaultDeviceName(), "device name recorded on the bridge (max 19 chars)")
	backend := fs.String("store", secrets.BackendAuto, "where to keep the key: auto, keyring or file")
	timeout := fs.Duration("timeout", 60*time.Second, "how long to wait for the link button")
	force := fs.Bool("force", false, "pair again even if this bridge already has a key")
	if err := fs.Parse(args); err != nil {
		return err
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	store, err := a.openStore(*backend)
	if err != nil {
		return err
	}

	// Step 1: decide which bridge to talk to.
	addr, expectedID, name, model, err := a.pickBridge(*ip, *id)
	if err != nil {
		return err
	}

	// Step 2: probe the public config endpoint. This is the first TLS
	// handshake: the certificate's CN must match the id (when known) and we
	// learn the fingerprint to pin.
	probe := hue.NewClient(hue.ClientOptions{Addr: addr, ID: expectedID, Log: a.log})
	info, err := probe.Info(a.ctx)
	if err != nil {
		return fmt.Errorf("cannot reach bridge at %s: %w", addr, err)
	}
	if expectedID != "" && info.ID != expectedID {
		return fmt.Errorf("bridge at %s reports id %s, expected %s", addr, info.ID, expectedID)
	}
	_, fp, ok := probe.SeenCertificate()
	if !ok {
		return errors.New("internal error: no certificate recorded")
	}
	if name == "" {
		name = info.Name
	}
	if model == "" {
		model = info.Model
	}

	// Step 3: refuse to silently create a second key for a paired bridge.
	if !*force {
		if _, err := hue.LoadCredentials(store, info.ID); err == nil {
			return fmt.Errorf("bridge %s already has a key in the %s; use --force to pair again", info.ID, store.Name())
		}
	}

	// Step 4: wait for the button.
	fmt.Printf("Bridge %s (%s, %s) at %s\n", name, info.ID, model, addr)
	fmt.Printf("Press the round link button on the bridge now. Waiting up to %s", timeout.Round(time.Second))
	client := hue.NewClient(hue.ClientOptions{Addr: addr, ID: info.ID, Fingerprint: fp, Log: a.log})
	creds, err := client.Pair(a.ctx, appName, *device, *timeout, func(int) { fmt.Print(".") })
	fmt.Println()
	if err != nil {
		return err
	}

	// Step 5: persist. Secret in the store, everything else in the config.
	if err := hue.SaveCredentials(store, info.ID, creds); err != nil {
		return err
	}
	cfg.Put(config.Bridge{
		ID: info.ID, Host: hostOf(addr), Port: portOf(addr),
		Name: name, Model: model, Fingerprint: fp, PairedAt: time.Now(),
	})
	if err := cfg.Save(); err != nil {
		return err
	}

	fmt.Printf("Paired. Application key %s… saved in the %s.\n", creds.AppKey[:4], store.Name())
	fmt.Printf("Bridge details and certificate fingerprint saved in %s.\n", cfg.Path())
	return nil
}

// pickBridge turns --ip/--id into a concrete address and, when known, id.
func (a *app) pickBridge(ip, id string) (addr, bridgeID, name, model string, err error) {
	if ip != "" {
		if _, _, splitErr := net.SplitHostPort(ip); splitErr != nil {
			ip = net.JoinHostPort(ip, "443")
		}
		return ip, strings.ToLower(id), "", "", nil
	}

	fmt.Fprintln(os.Stderr, "Looking for bridges...")
	bridges, err := hue.Discovery{Log: a.log}.Discover(a.ctx)
	if err != nil {
		return "", "", "", "", err
	}
	switch {
	case len(bridges) == 0:
		return "", "", "", "", errors.New("no bridge found; pass its address with --ip")
	case id != "":
		for _, b := range bridges {
			if b.ID == strings.ToLower(id) {
				return b.Addr(), b.ID, b.Name, b.Model, nil
			}
		}
		return "", "", "", "", fmt.Errorf("bridge %s not found on the network", id)
	case len(bridges) > 1:
		var ids []string
		for _, b := range bridges {
			ids = append(ids, b.ID+" ("+b.Addr()+")")
		}
		return "", "", "", "", fmt.Errorf("several bridges found, choose one with --id: %s", strings.Join(ids, ", "))
	default:
		b := bridges[0]
		return b.Addr(), b.ID, b.Name, b.Model, nil
	}
}

// authStatus implements `hue auth status`: lists known bridges and checks
// that their keys still work.
func (a *app) authStatus(args []string) error {
	fs := flag.NewFlagSet("hue auth status", flag.ContinueOnError)
	backend := fs.String("store", secrets.BackendAuto, "credential store to read: auto, keyring or file")
	offline := fs.Bool("offline", false, "do not contact the bridges")
	if err := fs.Parse(args); err != nil {
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
	if err := fs.Parse(args); err != nil {
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

// defaultDeviceName is the host name, trimmed to what the bridge accepts.
func defaultDeviceName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "computer"
	}
	host = strings.Split(host, ".")[0] // drop a domain suffix
	if len(host) > 19 {
		host = host[:19]
	}
	return host
}

func hostOf(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

func portOf(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 443
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 443
	}
	return n
}
