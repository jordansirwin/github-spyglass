package config

import (
	"os"
	"path/filepath"
	"strings"
)

// DefaultTokenPath returns where a token entered in the app is stored.
func DefaultTokenPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "github-spyglass-token"
	}
	return filepath.Join(home, ".github-spyglass", "token")
}

// LoadToken reads a stored token, or "" when there isn't one.
func LoadToken(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// SaveToken stores the token in a file only the current user can read.
func SaveToken(path, token string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(token+"\n"), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
