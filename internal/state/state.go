// Package state persists what the watcher has seen between polls.
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

// pruneAfter removes issues from state that haven't matched the filter in
// this long, keeping the state file from growing forever.
const pruneAfter = 30 * 24 * time.Hour

type State struct {
	Issues map[string]IssueState `json:"issues"`
}

type IssueState struct {
	UpdatedAt time.Time `json:"updated_at"`
	Comments  int       `json:"comments"`
	State     string    `json:"state"`
	Title     string    `json:"title"`
	LastSeen  time.Time `json:"last_seen"`
}

// New returns an empty state.
func New() *State {
	return &State{Issues: map[string]IssueState{}}
}

// Load reads the state file. firstRun is true when no state file exists yet,
// meaning the first poll should seed state without notifying.
func Load(path string) (st *State, firstRun bool, err error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return New(), true, nil
	}
	if err != nil {
		return nil, false, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, false, fmt.Errorf("parsing %s: %w", path, err)
	}
	if s.Issues == nil {
		s.Issues = map[string]IssueState{}
	}
	return &s, false, nil
}

// Save writes the state file atomically, creating parent directories as
// needed.
func Save(path string, st *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Prune drops issues that haven't matched the filter recently.
func (s *State) Prune(now time.Time) {
	for key, st := range s.Issues {
		if now.Sub(st.LastSeen) > pruneAfter {
			delete(s.Issues, key)
		}
	}
}

// DefaultPath returns the default state file location.
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "github-spyglass-state.json"
	}
	return filepath.Join(home, ".github-spyglass", "state.json")
}
