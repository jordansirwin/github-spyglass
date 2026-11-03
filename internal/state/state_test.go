package state

import (
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestLoadMissingFileIsFirstRun(t *testing.T) {
	st, firstRun, err := Load(filepath.Join(t.TempDir(), "nope", "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !firstRun {
		t.Error("missing file should report firstRun")
	}
	if st.Issues == nil {
		t.Error("Issues map should be initialized")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	now := time.Now().UTC().Truncate(time.Second)

	st := New()
	st.Issues["owner/repo#1"] = IssueState{
		UpdatedAt: now,
		Comments:  7,
		State:     "open",
		Title:     "a title",
		LastSeen:  now,
		Labels:    []string{"bug", "triage"},
		Assignees: []string{"alice"},
		Milestone: "v2.0",
		BodyHash:  "abcd1234",
	}
	if err := Save(path, st); err != nil {
		t.Fatal(err)
	}

	got, firstRun, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if firstRun {
		t.Error("existing file should not report firstRun")
	}
	if !reflect.DeepEqual(got.Issues["owner/repo#1"], st.Issues["owner/repo#1"]) {
		t.Errorf("round trip mismatch: got %+v want %+v", got.Issues["owner/repo#1"], st.Issues["owner/repo#1"])
	}
}

func TestPrune(t *testing.T) {
	now := time.Now().UTC()
	st := New()
	st.Issues["fresh"] = IssueState{LastSeen: now.Add(-time.Hour)}
	st.Issues["stale"] = IssueState{LastSeen: now.Add(-pruneAfter - time.Hour)}

	st.Prune(now)

	if _, ok := st.Issues["fresh"]; !ok {
		t.Error("fresh entry was pruned")
	}
	if _, ok := st.Issues["stale"]; ok {
		t.Error("stale entry was not pruned")
	}
}
