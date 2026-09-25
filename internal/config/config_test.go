package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFile(t *testing.T) {
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Bridges) != 0 || cfg.DefaultBridge != "" {
		t.Errorf("expected empty config, got %+v", cfg)
	}
	if _, err := cfg.Resolve(""); err == nil {
		t.Error("Resolve on empty config should fail")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "config.json")
	cfg, _ := LoadFrom(path)
	b1 := Bridge{ID: "aaa", Host: "10.0.0.1", Port: 443, Name: "One", Fingerprint: "sha256:1", PairedAt: time.Now().Round(time.Second)}
	b2 := Bridge{ID: "bbb", Host: "10.0.0.2", Port: 8443, Fingerprint: "sha256:2", PairedAt: time.Now().Round(time.Second)}
	cfg.Put(b1)
	cfg.Put(b2)
	if cfg.DefaultBridge != "aaa" {
		t.Errorf("first bridge should become default, got %q", cfg.DefaultBridge)
	}
	if err := cfg.Save(); err != nil {
		t.Fatal(err)
	}
	if info, _ := os.Stat(path); info.Mode().Perm() != 0o600 {
		t.Errorf("permissions %o", info.Mode().Perm())
	}

	again, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	got, err := again.Resolve("")
	if err != nil || !got.PairedAt.Equal(b1.PairedAt) || got.Addr() != "10.0.0.1:443" {
		t.Errorf("default = %+v, %v", got, err)
	}
	if got, _ := again.Resolve("bbb"); got.Addr() != "10.0.0.2:8443" {
		t.Errorf("bbb = %+v", got)
	}
	if _, err := again.Resolve("zzz"); err == nil {
		t.Error("unknown id should fail")
	}

	again.Remove("aaa")
	if again.DefaultBridge != "bbb" {
		t.Errorf("default should move to remaining bridge, got %q", again.DefaultBridge)
	}
	again.Remove("bbb")
	if again.DefaultBridge != "" {
		t.Errorf("default should clear, got %q", again.DefaultBridge)
	}
}
