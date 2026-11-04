package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestLoadMissingFileReturnsEmptyConfig(t *testing.T) {
	c, err := Load(filepath.Join(t.TempDir(), "nope", "config.json"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Query != "" || c.Interval != "" || c.Recent != 0 {
		t.Errorf("missing file should load as empty config, got %+v", c)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "config.json")
	c := &Config{Query: "repo:owner/repo is:issue", Interval: "15m", Recent: 25}
	if err := c.Save(path); err != nil {
		t.Fatal(err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if *got != *c {
		t.Errorf("round trip mismatch: got %+v want %+v", got, c)
	}
}

func TestPollInterval(t *testing.T) {
	for _, tc := range []struct {
		interval string
		want     time.Duration
	}{
		{"", 5 * time.Minute},        // default
		{"garbage", 5 * time.Minute}, // unparseable -> default
		{"-5m", 5 * time.Minute},     // non-positive -> default
		{"30s", time.Minute},         // clamped to the 1m floor
		{"15m", 15 * time.Minute},
		{"1h", time.Hour},
	} {
		c := &Config{Interval: tc.interval}
		if got := c.PollInterval(); got != tc.want {
			t.Errorf("PollInterval(%q) = %s, want %s", tc.interval, got, tc.want)
		}
	}
}

func TestRecentCount(t *testing.T) {
	for _, tc := range []struct {
		recent, want int
	}{
		{0, 10},  // default
		{-3, 10}, // non-positive -> default
		{5, 5},
		{50, 50},
		{999, MaxRecent}, // clamped to the slot ceiling
	} {
		c := &Config{Recent: tc.recent}
		if got := c.RecentCount(); got != tc.want {
			t.Errorf("RecentCount with Recent=%d = %d, want %d", tc.recent, got, tc.want)
		}
	}
}
