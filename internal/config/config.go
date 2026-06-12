// Package config persists aztui's local state: recently opened resources and
// (later) user settings. Stored as YAML under the user config dir.
package config

import (
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/GoosieZA/aztui/internal/azure"
)

const maxRecents = 15

// Recent is a previously opened resource, kept so the welcome screen can pin it.
type Recent struct {
	Resource   azure.Resource `yaml:"resource"`
	LastOpened time.Time      `yaml:"lastOpened"`
}

// SSHDefaults remembers the last successful SSH parameters so the connect
// form comes prefilled.
type SSHDefaults struct {
	User    string `yaml:"user,omitempty"`
	KeyPath string `yaml:"keyPath,omitempty"`
}

type Config struct {
	Recents []Recent    `yaml:"recents"`
	SSH     SSHDefaults `yaml:"ssh,omitempty"`
	// TileOrder pins the home-screen module tiles into a user-chosen order
	// (module IDs). Modules not listed sort by resource count after them.
	TileOrder []string `yaml:"tileOrder,omitempty"`
	// DisableUpdateCheck turns off the once-per-launch lookup of the latest
	// GitHub release.
	DisableUpdateCheck bool `yaml:"disableUpdateCheck,omitempty"`

	path string
}

// Dir returns aztui's config directory, creating it if needed.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "aztui")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Load reads the config file, returning an empty config if none exists yet.
func Load() (*Config, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	cfg := &Config{path: filepath.Join(dir, "config.yaml")}
	data, err := os.ReadFile(cfg.path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Save() error {
	data, err := yaml.Marshal(c)
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0o600)
}

// Touch records that a resource was just opened, moving it to the front of
// the recents list and persisting. Errors are returned but safe to ignore —
// recents are a convenience, not critical state.
func (c *Config) Touch(res azure.Resource) error {
	res.Properties = nil // properties can be large and may change; re-fetched on discovery
	recents := []Recent{{Resource: res, LastOpened: time.Now()}}
	for _, r := range c.Recents {
		if r.Resource.ID != res.ID {
			recents = append(recents, r)
		}
	}
	if len(recents) > maxRecents {
		recents = recents[:maxRecents]
	}
	c.Recents = recents
	return c.Save()
}

// IsRecent reports whether the resource ID is in the recents list.
func (c *Config) IsRecent(id string) bool {
	for _, r := range c.Recents {
		if r.Resource.ID == id {
			return true
		}
	}
	return false
}
