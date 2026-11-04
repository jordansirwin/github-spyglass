// Package config persists app settings for the menu bar app, where there are
// no command-line flags.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

type Config struct {
	// Query is the GitHub search filter to watch.
	Query string `json:"query"`
	// Interval is a Go duration string such as "5m". Empty means 5m.
	Interval string `json:"interval,omitempty"`
	// Token optionally overrides GitHub token resolution.
	Token string `json:"token,omitempty"`
}

// DefaultPath returns the default config file location.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "github-spyglass-config.json"
	}
	return filepath.Join(home, ".github-spyglass", "config.json")
}

// Load reads the config file, returning an empty config when the file
// doesn't exist yet.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return &c, nil
}

func (c *Config) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// PollInterval parses Interval, defaulting to 5 minutes and clamping to at
// least 1 minute (GitHub search rate limits).
func (c *Config) PollInterval() time.Duration {
	d, err := time.ParseDuration(c.Interval)
	if err != nil || d <= 0 {
		return 5 * time.Minute
	}
	if d < time.Minute {
		return time.Minute
	}
	return d
}
