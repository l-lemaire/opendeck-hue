package pairing

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/l-lemaire/opendeck-hue/internal/config"
	"github.com/l-lemaire/opendeck-hue/internal/hue/huetest"
	"github.com/l-lemaire/opendeck-hue/internal/secrets"
)

type memStore map[string]string

func (m memStore) Name() string { return "memory" }
func (m memStore) Get(k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", secrets.ErrNotFound
	}
	return v, nil
}
func (m memStore) Set(k, v string) error { m[k] = v; return nil }
func (m memStore) Delete(k string) error { delete(m, k); return nil }

const bridgeID = "001788fffe0a0b0c"

func TestRunPairsAndRecords(t *testing.T) {
	fb := huetest.New(t, bridgeID)
	fb.Refusals.Store(2)
	store := memStore{}
	cfgPath := filepath.Join(t.TempDir(), "config.json")

	var stages []string
	bridge, err := Run(context.Background(), Options{
		Addr: fb.Addr(), Store: store, ConfigPath: cfgPath, Timeout: 5 * time.Second,
		Report: func(p Progress) { stages = append(stages, p.Stage) },
	})
	if err != nil {
		t.Fatal(err)
	}
	if bridge.ID != bridgeID || bridge.Fingerprint != fb.Fingerprint || bridge.Name != "Fake Bridge" {
		t.Errorf("bridge = %+v", bridge)
	}
	if _, ok := store[bridgeID]; !ok {
		t.Error("credentials not stored")
	}
	cfg, _ := config.LoadFrom(cfgPath)
	if got, err := cfg.Resolve(""); err != nil || got.ID != bridgeID || got.Fingerprint != fb.Fingerprint {
		t.Errorf("config default bridge = %+v, %v", got, err)
	}
	// Order of stages, with at least one waiting report per refusal.
	if len(stages) < 5 || stages[0] != StageSearching || stages[1] != StageFound || stages[2] != StageWaiting || stages[len(stages)-1] != StagePaired {
		t.Errorf("stages = %v", stages)
	}

	// Second run without Force is refused before touching the button.
	_, err = Run(context.Background(), Options{Addr: fb.Addr(), Store: store, ConfigPath: cfgPath})
	if !errors.Is(err, ErrAlreadyPaired) {
		t.Errorf("second pairing: got %v, want ErrAlreadyPaired", err)
	}
}

func TestRunRejectsWrongID(t *testing.T) {
	fb := huetest.New(t, bridgeID)
	_, err := Run(context.Background(), Options{
		Addr: fb.Addr(), ID: "ecb5fafffe000000", Store: memStore{}, ConfigPath: filepath.Join(t.TempDir(), "c.json"),
	})
	if err == nil {
		t.Fatal("expected a certificate/id mismatch error")
	}
}

func TestRunTimesOut(t *testing.T) {
	fb := huetest.New(t, bridgeID)
	fb.Refusals.Store(1000)
	_, err := Run(context.Background(), Options{
		Addr: fb.Addr(), Store: memStore{}, ConfigPath: filepath.Join(t.TempDir(), "c.json"), Timeout: 1500 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("expected a timeout error")
	}
}
