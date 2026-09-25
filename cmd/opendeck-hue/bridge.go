package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"

	"github.com/l-lemaire/opendeck-hue/internal/config"
	"github.com/l-lemaire/opendeck-hue/internal/hue"
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

	dir, err := config.Dir()
	if err != nil {
		return nil, config.Bridge{}, err
	}
	store, err := secrets.Open(secrets.BackendAuto, filepath.Join(dir, "credentials.json"), f.log)
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
