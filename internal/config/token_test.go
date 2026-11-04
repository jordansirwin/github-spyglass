package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestTokenStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "token")
	if got := LoadToken(path); got != "" {
		t.Errorf("missing file: got %q", got)
	}
	if err := SaveToken(path, "ghp_secret"); err != nil {
		t.Fatal(err)
	}
	if got := LoadToken(path); got != "ghp_secret" {
		t.Errorf("got %q", got)
	}
	// Overwriting keeps the restrictive mode.
	if err := SaveToken(path, "ghp_other"); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("token file mode = %o, want 600", perm)
	}
	if got := LoadToken(path); got != "ghp_other" {
		t.Errorf("after overwrite got %q", got)
	}
}
