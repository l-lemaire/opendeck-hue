// Package pairing is the one place that knows how to pair with a bridge:
// find it, verify its certificate, wait for the link button, store the key
// and record the bridge. The CLI (`hue auth`) and the plugin (the "Pair"
// button in the key panel) both call Run, and only differ in how they show
// progress.
package pairing

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/l-lemaire/opendeck-hue/internal/config"
	"github.com/l-lemaire/opendeck-hue/internal/hue"
	"github.com/l-lemaire/opendeck-hue/internal/secrets"
)

// AppName is the first half of the "devicetype" the bridge records for the
// key; it shows up in the Hue app under Settings > Apps.
const AppName = "opendeck-hue"

// DefaultTimeout is how long Run waits for the link button.
const DefaultTimeout = 60 * time.Second

// Stage names reported through Options.Report, in the order they happen.
const (
	StageSearching = "searching" // looking for a bridge on the network
	StageFound     = "found"     // bridge identified, certificate checked
	StageWaiting   = "waiting"   // polling until the button is pressed
	StagePaired    = "paired"    // key stored, bridge recorded
)

// Progress is one step of the flow, for display.
type Progress struct {
	Stage       string
	Message     string
	SecondsLeft int // meaningful during StageWaiting
}

// Options configures one pairing run.
type Options struct {
	// Addr is a manual "host" or "host:port"; empty means discover.
	Addr string
	// ID selects a bridge when discovery finds several. With Addr it is
	// only checked against what the bridge reports.
	ID string
	// DeviceName is the second half of the devicetype (max 19 chars).
	// Empty means the host name.
	DeviceName string
	// Store receives the credentials. Required.
	Store CredentialStore
	// ConfigPath overrides the config file location (tests). Empty means
	// the default.
	ConfigPath string
	// Timeout for the button. Zero means DefaultTimeout.
	Timeout time.Duration
	// Force pairs again even if the store already has a key for the bridge.
	Force bool
	// Report, if set, is called at each stage. It must return quickly.
	Report func(Progress)
	Log    *log.Logger
}

// CredentialStore is where the key goes; secrets.Open provides one.
type CredentialStore = secrets.Store

// ErrAlreadyPaired is returned when the bridge has a key and Force is off.
var ErrAlreadyPaired = errors.New("bridge already paired")

// Run performs the whole flow and returns the recorded bridge.
func Run(ctx context.Context, o Options) (config.Bridge, error) {
	if o.Store == nil {
		return config.Bridge{}, errors.New("pairing: no credential store")
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = DefaultTimeout
	}
	device := o.DeviceName
	if device == "" {
		device = defaultDeviceName()
	}
	report := o.Report
	if report == nil {
		report = func(Progress) {}
	}

	cfg, err := loadConfig(o.ConfigPath)
	if err != nil {
		return config.Bridge{}, err
	}

	// Step 1: decide which bridge to talk to.
	report(Progress{Stage: StageSearching, Message: "Looking for a bridge on the network…"})
	addr, expectedID, name, model, err := pickBridge(ctx, o)
	if err != nil {
		return config.Bridge{}, err
	}

	// Step 2: probe the public config endpoint. This is the first TLS
	// handshake: the certificate's CN must match the id (when known) and we
	// learn the fingerprint to pin.
	probe := hue.NewClient(hue.ClientOptions{Addr: addr, ID: expectedID, Log: o.Log})
	info, err := probe.Info(ctx)
	if err != nil {
		return config.Bridge{}, fmt.Errorf("cannot reach bridge at %s: %w", addr, err)
	}
	if expectedID != "" && info.ID != expectedID {
		return config.Bridge{}, fmt.Errorf("bridge at %s reports id %s, expected %s", addr, info.ID, expectedID)
	}
	_, fp, ok := probe.SeenCertificate()
	if !ok {
		return config.Bridge{}, errors.New("internal error: no certificate recorded")
	}
	if name == "" {
		name = info.Name
	}
	if model == "" {
		model = info.Model
	}
	bridge := config.Bridge{
		ID: info.ID, Host: hostOf(addr), Port: portOf(addr),
		Name: name, Model: model, Fingerprint: fp,
	}
	report(Progress{Stage: StageFound, Message: fmt.Sprintf("Found %s (%s) at %s", name, info.ID, addr)})

	// Step 3: refuse to silently create a second key for a paired bridge.
	if !o.Force {
		if _, err := hue.LoadCredentials(o.Store, info.ID); err == nil {
			return bridge, fmt.Errorf("%w: %s already has a key in the %s", ErrAlreadyPaired, info.ID, o.Store.Name())
		}
	}

	// Step 4: wait for the button.
	deadline := time.Now().Add(timeout)
	report(Progress{Stage: StageWaiting, Message: "Press the round button on the bridge", SecondsLeft: int(timeout.Seconds())})
	client := hue.NewClient(hue.ClientOptions{Addr: addr, ID: info.ID, Fingerprint: fp, Log: o.Log})
	creds, err := client.Pair(ctx, AppName, device, timeout, func(int) {
		left := int(time.Until(deadline).Seconds() + 0.5)
		if left < 0 {
			left = 0
		}
		report(Progress{Stage: StageWaiting, Message: "Press the round button on the bridge", SecondsLeft: left})
	})
	if err != nil {
		return bridge, err
	}

	// Step 5: persist. Secret in the store, everything else in the config.
	if err := hue.SaveCredentials(o.Store, info.ID, creds); err != nil {
		return bridge, err
	}
	bridge.PairedAt = time.Now()
	cfg.Put(bridge)
	if err := cfg.Save(); err != nil {
		return bridge, err
	}
	report(Progress{Stage: StagePaired, Message: fmt.Sprintf("Paired with %s. Key saved in the %s.", name, o.Store.Name())})
	return bridge, nil
}

// pickBridge turns Addr/ID into a concrete address and, when known, id.
func pickBridge(ctx context.Context, o Options) (addr, bridgeID, name, model string, err error) {
	if o.Addr != "" {
		addr = o.Addr
		if _, _, splitErr := net.SplitHostPort(addr); splitErr != nil {
			addr = net.JoinHostPort(addr, "443")
		}
		return addr, strings.ToLower(o.ID), "", "", nil
	}

	bridges, err := hue.Discovery{Log: o.Log}.Discover(ctx)
	if err != nil {
		return "", "", "", "", err
	}
	id := strings.ToLower(o.ID)
	switch {
	case len(bridges) == 0:
		return "", "", "", "", errors.New("no bridge found on the network; give its address")
	case id != "":
		for _, b := range bridges {
			if b.ID == id {
				return b.Addr(), b.ID, b.Name, b.Model, nil
			}
		}
		return "", "", "", "", fmt.Errorf("bridge %s not found on the network", id)
	case len(bridges) > 1:
		var ids []string
		for _, b := range bridges {
			ids = append(ids, b.ID+" ("+b.Addr()+")")
		}
		return "", "", "", "", fmt.Errorf("several bridges found, choose one: %s", strings.Join(ids, ", "))
	default:
		b := bridges[0]
		return b.Addr(), b.ID, b.Name, b.Model, nil
	}
}

func loadConfig(path string) (*config.Config, error) {
	if path != "" {
		return config.LoadFrom(path)
	}
	return config.Load()
}

// defaultDeviceName is the host name, trimmed to what the bridge accepts.
func defaultDeviceName() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "computer"
	}
	host = strings.Split(host, ".")[0]
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
