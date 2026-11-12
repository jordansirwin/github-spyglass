package state

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestEventsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "events.json")
	events, err := LoadEvents(path)
	if err != nil || len(events) != 0 {
		t.Fatalf("missing file: events=%v err=%v", events, err)
	}

	want := []Event{
		{When: time.Date(2026, 11, 5, 8, 0, 0, 0, time.UTC), Label: "New comment on a/b#1", URL: "https://example.com/1", DiffPath: "/tmp/d.html"},
		{When: time.Date(2026, 11, 5, 7, 0, 0, 0, time.UTC), Label: "Closed: a/b#2"},
	}
	if err := SaveEvents(path, want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadEvents(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !got[0].When.Equal(want[0].When) || got[0].Label != want[0].Label ||
		got[0].URL != want[0].URL || got[0].DiffPath != want[0].DiffPath || got[1].DiffPath != "" || got[1].Label != want[1].Label || got[1].URL != "" {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

func TestLoadEventsRejectsCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadEvents(path); err == nil {
		t.Fatal("want error for corrupt file")
	}
}
