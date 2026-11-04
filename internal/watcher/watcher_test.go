package watcher

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jirwin/github-spyglass/internal/github"
	"github.com/jirwin/github-spyglass/internal/state"
)

type fakeGitHub struct {
	issues   []github.Issue
	comments []github.Comment
}

func (f *fakeGitHub) SearchIssues(ctx context.Context, query string) ([]github.Issue, error) {
	return f.issues, nil
}

func (f *fakeGitHub) CommentsSince(ctx context.Context, issue github.Issue, since time.Time) ([]github.Comment, error) {
	return f.comments, nil
}

type notification struct {
	title, subtitle, message, url string
}

type fakeNotifier struct {
	sent []notification
}

func (f *fakeNotifier) Notify(title, subtitle, message, url string) error {
	f.sent = append(f.sent, notification{title, subtitle, message, url})
	return nil
}

func testIssue(number int, updatedAt time.Time, comments int, issueState string) github.Issue {
	return github.Issue{
		Number:        number,
		Title:         fmt.Sprintf("Test issue %d", number),
		HTMLURL:       fmt.Sprintf("https://github.com/owner/repo/issues/%d", number),
		UpdatedAt:     updatedAt,
		Comments:      comments,
		State:         issueState,
		RepositoryURL: "https://api.github.com/repos/owner/repo",
	}
}

func newWatcher(gh *fakeGitHub, n *fakeNotifier) *Watcher {
	return &Watcher{Client: gh, Notifier: n, Query: "repo:owner/repo is:issue"}
}

func TestSeedRunDoesNotNotify(t *testing.T) {
	gh := &fakeGitHub{issues: []github.Issue{testIssue(1, time.Now(), 3, "open")}}
	n := &fakeNotifier{}
	st := state.New()

	if err := newWatcher(gh, n).Poll(context.Background(), st, true); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 0 {
		t.Fatalf("seed run sent %d notifications, want 0: %+v", len(n.sent), n.sent)
	}
	if _, ok := st.Issues["owner/repo#1"]; !ok {
		t.Fatal("seed run did not record issue state")
	}
}

func TestUnchangedIssueDoesNotNotify(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	gh := &fakeGitHub{issues: []github.Issue{testIssue(1, now, 3, "open")}}
	n := &fakeNotifier{}
	st := state.New()
	w := newWatcher(gh, n)

	ctx := context.Background()
	if err := w.Poll(ctx, st, true); err != nil {
		t.Fatal(err)
	}
	if err := w.Poll(ctx, st, false); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 0 {
		t.Fatalf("unchanged issue sent %d notifications, want 0: %+v", len(n.sent), n.sent)
	}
}

func TestNewCommentNotifies(t *testing.T) {
	before := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	after := before.Add(30 * time.Minute)

	gh := &fakeGitHub{issues: []github.Issue{testIssue(1, before, 3, "open")}}
	n := &fakeNotifier{}
	st := state.New()
	w := newWatcher(gh, n)
	ctx := context.Background()

	if err := w.Poll(ctx, st, true); err != nil {
		t.Fatal(err)
	}

	gh.issues = []github.Issue{testIssue(1, after, 4, "open")}
	gh.comments = []github.Comment{{
		HTMLURL:   "https://github.com/owner/repo/issues/1#issuecomment-99",
		Body:      "line one\nline two of the comment",
		CreatedAt: after,
		User:      github.User{Login: "alice"},
	}}
	if err := w.Poll(ctx, st, false); err != nil {
		t.Fatal(err)
	}

	if len(n.sent) != 1 {
		t.Fatalf("got %d notifications, want 1: %+v", len(n.sent), n.sent)
	}
	got := n.sent[0]
	if got.title != "New comment on owner/repo#1" {
		t.Errorf("title = %q", got.title)
	}
	if want := "alice: line one line two of the comment"; got.message != want {
		t.Errorf("message = %q, want %q", got.message, want)
	}
	if got.url != "https://github.com/owner/repo/issues/1#issuecomment-99" {
		t.Errorf("url = %q, want the comment deep link", got.url)
	}
}

func TestStateChangeNotifies(t *testing.T) {
	before := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	gh := &fakeGitHub{issues: []github.Issue{testIssue(1, before, 0, "open")}}
	n := &fakeNotifier{}
	st := state.New()
	w := newWatcher(gh, n)
	ctx := context.Background()

	if err := w.Poll(ctx, st, true); err != nil {
		t.Fatal(err)
	}

	gh.issues = []github.Issue{testIssue(1, before.Add(time.Minute), 0, "closed")}
	if err := w.Poll(ctx, st, false); err != nil {
		t.Fatal(err)
	}

	if len(n.sent) != 1 {
		t.Fatalf("got %d notifications, want 1: %+v", len(n.sent), n.sent)
	}
	if got := n.sent[0].title; got != "Closed: owner/repo#1" {
		t.Errorf("title = %q, want Closed: owner/repo#1", got)
	}
}

func TestEditNotifies(t *testing.T) {
	before := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	gh := &fakeGitHub{issues: []github.Issue{testIssue(1, before, 2, "open")}}
	n := &fakeNotifier{}
	st := state.New()
	w := newWatcher(gh, n)
	ctx := context.Background()

	if err := w.Poll(ctx, st, true); err != nil {
		t.Fatal(err)
	}

	// updated_at advances with no comment or state change: an edit.
	gh.issues = []github.Issue{testIssue(1, before.Add(time.Minute), 2, "open")}
	if err := w.Poll(ctx, st, false); err != nil {
		t.Fatal(err)
	}

	if len(n.sent) != 1 {
		t.Fatalf("got %d notifications, want 1: %+v", len(n.sent), n.sent)
	}
	if got := n.sent[0].title; got != "Updated: owner/repo#1" {
		t.Errorf("title = %q, want Updated: owner/repo#1", got)
	}
}

func TestNewMatchNotifies(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	gh := &fakeGitHub{issues: []github.Issue{testIssue(1, now, 0, "open")}}
	n := &fakeNotifier{}
	st := state.New()
	w := newWatcher(gh, n)
	ctx := context.Background()

	if err := w.Poll(ctx, st, true); err != nil {
		t.Fatal(err)
	}

	gh.issues = append(gh.issues, testIssue(2, now, 0, "open"))
	if err := w.Poll(ctx, st, false); err != nil {
		t.Fatal(err)
	}

	if len(n.sent) != 1 {
		t.Fatalf("got %d notifications, want 1: %+v", len(n.sent), n.sent)
	}
	if got := n.sent[0].title; got != "New issue matches filter" {
		t.Errorf("title = %q", got)
	}
}

func TestNotificationCap(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	gh := &fakeGitHub{}
	n := &fakeNotifier{}
	st := state.New()
	w := newWatcher(gh, n)
	ctx := context.Background()

	if err := w.Poll(ctx, st, true); err != nil {
		t.Fatal(err)
	}

	for i := 1; i <= maxNotificationsPerPoll+5; i++ {
		gh.issues = append(gh.issues, testIssue(i, now, 0, "open"))
	}
	if err := w.Poll(ctx, st, false); err != nil {
		t.Fatal(err)
	}

	if len(n.sent) != maxNotificationsPerPoll {
		t.Fatalf("got %d notifications, want cap of %d", len(n.sent), maxNotificationsPerPoll)
	}
	last := n.sent[len(n.sent)-1]
	if !strings.Contains(last.message, "suppressed") {
		t.Errorf("last notification should mention suppression, got %q", last.message)
	}
}

func TestLabelAndAssigneeChangeNotifies(t *testing.T) {
	before := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	issue := testIssue(1, before, 0, "open")
	issue.Labels = []github.Label{{Name: "bug"}, {Name: "needs-info"}}
	issue.Assignees = []github.User{{Login: "bob"}}

	gh := &fakeGitHub{issues: []github.Issue{issue}}
	n := &fakeNotifier{}
	st := state.New()
	w := newWatcher(gh, n)
	ctx := context.Background()

	if err := w.Poll(ctx, st, true); err != nil {
		t.Fatal(err)
	}

	changed := testIssue(1, before.Add(time.Minute), 0, "open")
	changed.Labels = []github.Label{{Name: "bug"}, {Name: "triage"}}
	changed.Assignees = []github.User{{Login: "bob"}, {Login: "alice"}}
	gh.issues = []github.Issue{changed}
	if err := w.Poll(ctx, st, false); err != nil {
		t.Fatal(err)
	}

	if len(n.sent) != 1 {
		t.Fatalf("got %d notifications, want 1: %+v", len(n.sent), n.sent)
	}
	got := n.sent[0]
	if got.title != "Updated: owner/repo#1" {
		t.Errorf("title = %q", got.title)
	}
	if want := "labels +triage -needs-info, assignees +alice"; got.message != want {
		t.Errorf("message = %q, want %q", got.message, want)
	}
}

func TestRenameAndBodyEditNotifies(t *testing.T) {
	before := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	issue := testIssue(1, before, 0, "open")
	issue.Body = "original description"

	gh := &fakeGitHub{issues: []github.Issue{issue}}
	n := &fakeNotifier{}
	st := state.New()
	w := newWatcher(gh, n)
	ctx := context.Background()

	if err := w.Poll(ctx, st, true); err != nil {
		t.Fatal(err)
	}

	changed := testIssue(1, before.Add(time.Minute), 0, "open")
	changed.Title = "A better title"
	changed.Body = "rewritten description"
	gh.issues = []github.Issue{changed}
	if err := w.Poll(ctx, st, false); err != nil {
		t.Fatal(err)
	}

	if len(n.sent) != 1 {
		t.Fatalf("got %d notifications, want 1: %+v", len(n.sent), n.sent)
	}
	if want := `renamed to "A better title", description edited`; n.sent[0].message != want {
		t.Errorf("message = %q, want %q", n.sent[0].message, want)
	}
}

func TestMilestoneChangeNotifies(t *testing.T) {
	before := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	gh := &fakeGitHub{issues: []github.Issue{testIssue(1, before, 0, "open")}}
	n := &fakeNotifier{}
	st := state.New()
	w := newWatcher(gh, n)
	ctx := context.Background()

	if err := w.Poll(ctx, st, true); err != nil {
		t.Fatal(err)
	}

	set := testIssue(1, before.Add(time.Minute), 0, "open")
	set.Milestone = &github.Milestone{Title: "v2.0"}
	gh.issues = []github.Issue{set}
	if err := w.Poll(ctx, st, false); err != nil {
		t.Fatal(err)
	}

	removed := testIssue(1, before.Add(2*time.Minute), 0, "open")
	gh.issues = []github.Issue{removed}
	if err := w.Poll(ctx, st, false); err != nil {
		t.Fatal(err)
	}

	if len(n.sent) != 2 {
		t.Fatalf("got %d notifications, want 2: %+v", len(n.sent), n.sent)
	}
	if want := "milestone set to v2.0"; n.sent[0].message != want {
		t.Errorf("message = %q, want %q", n.sent[0].message, want)
	}
	if want := "milestone removed"; n.sent[1].message != want {
		t.Errorf("message = %q, want %q", n.sent[1].message, want)
	}
}

func TestLegacyStateEntrySkipsSnapshotDiff(t *testing.T) {
	before := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	issue := testIssue(1, before.Add(time.Minute), 0, "open")
	issue.Labels = []github.Label{{Name: "bug"}}

	gh := &fakeGitHub{issues: []github.Issue{issue}}
	n := &fakeNotifier{}
	// Simulate a state entry written before snapshot fields existed.
	st := state.New()
	st.Issues["owner/repo#1"] = state.IssueState{
		UpdatedAt: before,
		State:     "open",
		Title:     issue.Title,
		LastSeen:  before,
	}

	if err := newWatcher(gh, n).Poll(context.Background(), st, false); err != nil {
		t.Fatal(err)
	}

	if len(n.sent) != 1 {
		t.Fatalf("got %d notifications, want 1: %+v", len(n.sent), n.sent)
	}
	if want := "Issue was edited, labeled, or otherwise changed"; n.sent[0].message != want {
		t.Errorf("message = %q, want generic fallback %q", n.sent[0].message, want)
	}
}

func TestCondense(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		max      int
	}{
		{"hello world", "hello world", 50},
		{"line one\n\nline two\t end", "line one line two end", 50},
		{"aaaa bbbb", "aaaa…", 5},
		{"", "", 10},
	} {
		if got := condense(tc.in, tc.max); got != tc.want {
			t.Errorf("condense(%q, %d) = %q, want %q", tc.in, tc.max, got, tc.want)
		}
	}
}
