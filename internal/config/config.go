// Package config persists the non-secret state of the CLI and plugin: which
// bridges are known, where they are, which certificate to expect, and which
// one is the default. Secrets live in package secrets, never here.
//
// The file is JSON at $XDG_CONFIG_HOME/hue/config.json, which on Linux
// resolves to ~/.config/hue/config.json. It is safe to read, share and edit
// by hand.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// Bridge is one paired (or about to be paired) bridge.
type Bridge struct {
	ID          string    `json:"id"`
	Host        string    `json:"host"`
	Port        int       `json:"port"`
	Name        string    `json:"name,omitempty"`
	Model       string    `json:"model,omitempty"`
	Fingerprint string    `json:"cert_fingerprint"`
	PairedAt    time.Time `json:"paired_at"`
}

// Addr returns host:port.
func (b Bridge) Addr() string {
	return b.Host + ":" + strconv.Itoa(b.Port)
}

// Config is the whole file.
type Config struct {
	Version       int               `json:"version"`
	DefaultBridge string            `json:"default_bridge,omitempty"`
	Bridges       map[string]Bridge `json:"bridges"`
	// PluginDebug makes the OpenDeck plugin write full debug output
	// (protocol messages, HTTP dumps) to its log file. The plugin has no
	// command line of its own, so this is how --debug reaches it.
	PluginDebug bool `json:"plugin_debug,omitempty"`

	path string // where it was loaded from; not serialised (lower-case field)
}

// Dir returns the directory holding config.json and the credentials
// fallback file. It honours $XDG_CONFIG_HOME through os.UserConfigDir.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "hue"), nil
}

// PluginLogPath returns the plugin's log file, under $XDG_STATE_HOME
// (default ~/.local/state), the XDG location for logs and other state that
// is neither configuration nor cache.
func PluginLogPath() (string, error) {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "opendeck-hue", "plugin.log"), nil
}

// Load reads the config file. A missing file yields an empty, usable Config.
func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	return LoadFrom(filepath.Join(dir, "config.json"))
}

// LoadFrom is Load with an explicit path, for tests and unusual setups.
func LoadFrom(path string) (*Config, error) {
	cfg := &Config{Version: 1, Bridges: map[string]Bridge{}, path: path}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := json.Unmarshal(raw, cfg); err != nil {
		return nil, fmt.Errorf("config %s is corrupt: %w", path, err)
	}
	if cfg.Bridges == nil {
		cfg.Bridges = map[string]Bridge{}
	}
	return cfg, nil
}

// Path returns where Save will write.
func (c *Config) Path() string { return c.path }

// Save writes the file with owner-only permissions. Nothing in it is secret,
// but there is no reason for other users to read it either.
func (c *Config) Save() error {
	if err := os.MkdirAll(filepath.Dir(c.path), 0o700); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, append(raw, '\n'), 0o600)
}

// Put adds or replaces a bridge and makes it the default if none is set.
func (c *Config) Put(b Bridge) {
	c.Bridges[b.ID] = b
	if c.DefaultBridge == "" {
		c.DefaultBridge = b.ID
	}
}

// Remove forgets a bridge, moving the default to another one if needed.
func (c *Config) Remove(id string) {
	delete(c.Bridges, id)
	if c.DefaultBridge == id {
		c.DefaultBridge = ""
		for other := range c.Bridges {
			c.DefaultBridge = other
			break
		}
	}
}

// Resolve returns the bridge with the given id, or the default when id is
// empty. The error explains what to do when nothing matches.
func (c *Config) Resolve(id string) (Bridge, error) {
	if id == "" {
		id = c.DefaultBridge
	}
	if id == "" {
		return Bridge{}, errors.New("no bridge paired yet; run `hue auth`")
	}
	b, ok := c.Bridges[id]
	if !ok {
		return Bridge{}, fmt.Errorf("bridge %s is not paired; run `hue auth` or check `hue auth status`", id)
	}
	return b, nil
}
