package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// Event is one entry in the menu bar's recent-updates list.
type Event struct {
	When  time.Time `json:"when"`
	Label string    `json:"label"`
	URL   string    `json:"url,omitempty"`
}

// DefaultEventsPath returns the default recent-updates file location. It is
// separate from the state file so the CLI, which shares that one, never
// touches it.
func DefaultEventsPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "github-spyglass-events.json"
	}
	return filepath.Join(home, ".github-spyglass", "events.json")
}

// LoadEvents reads the recent-updates list, newest first. A missing file is
// an empty list.
func LoadEvents(path string) ([]Event, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var events []Event
	if err := json.Unmarshal(data, &events); err != nil {
		return nil, fmt.Errorf("parsing %s: %w", path, err)
	}
	return events, nil
}

// SaveEvents writes the recent-updates list atomically.
func SaveEvents(path string, events []Event) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(events, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
