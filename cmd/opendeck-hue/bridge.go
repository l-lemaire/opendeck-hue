package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"

	"github.com/l-lemaire/opendeck-hue/internal/config"
	"github.com/l-lemaire/opendeck-hue/internal/hue"
	"github.com/l-lemaire/opendeck-hue/internal/pairing"
	"github.com/l-lemaire/opendeck-hue/internal/secrets"
)

// bridgeConnector hands out a ready hue.Client for a bridge id (empty for
// the default). It reads the same config file and credential store as the
// CLI, and caches one client per bridge so the keyring is consulted once,
// not on every key press.
//
// It is an interface so tests can substitute a connector that points at a
// fake bridge without a config file or a keyring.
type bridgeConnector interface {
	Connect(ctx context.Context, bridgeID string) (*hue.Client, config.Bridge, error)
	// Bridges lists the known bridges for the property inspector.
	Bridges() ([]config.Bridge, string, error)
	// Discover finds bridges on the network, for the pairing wizard.
	Discover(ctx context.Context) ([]hue.Bridge, error)
	// Pair runs the pairing flow (see internal/pairing), reporting progress.
	// addr and id are optional: an address skips discovery, an id checks
	// the bridge is the one the user chose.
	Pair(ctx context.Context, addr, id string, report func(pairing.Progress)) (config.Bridge, error)
}

// fileConnector is the real implementation backed by ~/.config/hue.
type fileConnector struct {
	log *log.Logger

	mu      sync.Mutex
	clients map[string]*hue.Client
}

func newFileConnector(debug *log.Logger) *fileConnector {
	return &fileConnector{log: debug, clients: map[string]*hue.Client{}}
}

func (f *fileConnector) Bridges() ([]config.Bridge, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, "", err
	}
	bridges := make([]config.Bridge, 0, len(cfg.Bridges))
	for _, b := range cfg.Bridges {
		bridges = append(bridges, b)
	}
	return bridges, cfg.DefaultBridge, nil
}

// Discover runs mDNS discovery with the cloud fallback.
func (f *fileConnector) Discover(ctx context.Context) ([]hue.Bridge, error) {
	return hue.Discovery{Log: f.log}.Discover(ctx)
}

// Pair pairs with a bridge and stores the key in the same credential store
// the CLI uses.
func (f *fileConnector) Pair(ctx context.Context, addr, id string, report func(pairing.Progress)) (config.Bridge, error) {
	store, err := f.store()
	if err != nil {
		return config.Bridge{}, err
	}
	return pairing.Run(ctx, pairing.Options{Addr: addr, ID: id, Store: store, Report: report, Log: f.log})
}

// store opens the credential store: keyring, or the file fallback next to
// the config file.
func (f *fileConnector) store() (secrets.Store, error) {
	dir, err := config.Dir()
	if err != nil {
		return nil, err
	}
	return secrets.Open(secrets.BackendAuto, filepath.Join(dir, "credentials.json"), f.log)
}

func (f *fileConnector) Connect(ctx context.Context, bridgeID string) (*hue.Client, config.Bridge, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, config.Bridge{}, err
	}
	b, err := cfg.Resolve(bridgeID)
	if err != nil {
		return nil, config.Bridge{}, err
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	if c, ok := f.clients[b.ID]; ok {
		return c, b, nil
	}

	store, err := f.store()
	if err != nil {
		return nil, config.Bridge{}, err
	}
	creds, err := hue.LoadCredentials(store, b.ID)
	if err != nil {
		return nil, config.Bridge{}, fmt.Errorf("%w (pair with `hue auth`)", err)
	}
	c := hue.NewClient(hue.ClientOptions{
		Addr: b.Addr(), ID: b.ID, Fingerprint: b.Fingerprint, AppKey: creds.AppKey, Log: f.log,
	})
	f.clients[b.ID] = c
	return c, b, nil
}
