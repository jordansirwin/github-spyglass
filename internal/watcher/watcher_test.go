package watcher

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/jirwin/github-spyglass/internal/diff"
	"github.com/jirwin/github-spyglass/internal/github"
	"github.com/jirwin/github-spyglass/internal/state"
)

type fakeGitHub struct {
	issues   []github.Issue
	comments []github.Comment

	revisions    []github.Revision // description history, newest first
	historyErr   error
	historyCalls int
}

func (f *fakeGitHub) DescriptionHistory(ctx context.Context, issue github.Issue, limit int) ([]github.Revision, error) {
	f.historyCalls++
	return f.revisions, f.historyErr
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

// fakeDiffNotifier also supports the "view diff" action.
type fakeDiffNotifier struct {
	fakeNotifier
	changes []diff.Change
}

func (f *fakeDiffNotifier) NotifyDescriptionChange(title, subtitle, message, url string, change diff.Change) error {
	f.sent = append(f.sent, notification{title, subtitle, message, url})
	f.changes = append(f.changes, change)
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

// rev builds a description revision.
func rev(at time.Time, editor, body string) github.Revision {
	return github.Revision{EditedAt: at, Editor: editor, Body: body, HasBody: true}
}

// pollEdit seeds an issue, then polls again after it was edited (the edit
// callback may change the issue). revs is the history GitHub reports.
func pollEdit(t *testing.T, n Notifier, revs []github.Revision, edit func(*github.Issue)) *fakeGitHub {
	t.Helper()
	before := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
	issue := testIssue(1, before, 0, "open")
	issue.Body = "original description"
	gh := &fakeGitHub{issues: []github.Issue{issue}, revisions: revs}
	st := state.New()
	w := &Watcher{Client: gh, Notifier: n, Query: "repo:owner/repo is:issue"}
	if err := w.Poll(context.Background(), st, true); err != nil {
		t.Fatal(err)
	}
	changed := testIssue(1, before.Add(time.Minute), 0, "open")
	changed.Body = "rewritten description"
	if edit != nil {
		edit(&changed)
	}
	gh.issues = []github.Issue{changed}
	if err := w.Poll(context.Background(), st, false); err != nil {
		t.Fatal(err)
	}
	return gh
}

// editTime is a time after the seeded poll's updated_at (an hour ago) so
// revisions at editTime(n) count as edits made since the issue was last seen.
func editTime(minutes int) time.Time {
	return time.Now().UTC().Add(-time.Hour).Truncate(time.Second).Add(time.Duration(minutes) * time.Minute)
}

func TestDescriptionEditAttachesDiffFromHistory(t *testing.T) {
	n := &fakeDiffNotifier{}
	pollEdit(t, n, []github.Revision{
		rev(editTime(1), "alice", "rewritten description"),
		rev(editTime(-30), "alice", "original description"),
	}, nil)

	if len(n.sent) != 1 || len(n.changes) != 1 {
		t.Fatalf("sent=%d changes=%d, want 1 each", len(n.sent), len(n.changes))
	}
	c := n.changes[0]
	if c.Key != "owner/repo#1" || c.Title != "Test issue 1" || !strings.HasSuffix(c.URL, "/issues/1") {
		t.Errorf("change identity = %+v", c)
	}
	if len(c.Edits) != 1 {
		t.Fatalf("edits = %d, want 1", len(c.Edits))
	}
	e := c.Edits[0]
	if e.Old != "original description" || e.New != "rewritten description" || e.Editor != "alice" || !e.Known {
		t.Errorf("edit = %+v", e)
	}
}

func TestEverySinceLastSeenEditIsListedNewestFirst(t *testing.T) {
	n := &fakeDiffNotifier{}
	pollEdit(t, n, []github.Revision{
		rev(editTime(5), "carol", "v4"),
		rev(editTime(3), "bob", "v3"),
		rev(editTime(1), "alice", "v2"),
		rev(editTime(-30), "alice", "v1"), // an edit before the last poll: already seen
		rev(editTime(-60), "alice", "v0"),
	}, nil)

	if len(n.changes) != 1 {
		t.Fatalf("changes = %d", len(n.changes))
	}
	edits := n.changes[0].Edits
	if len(edits) != 3 {
		t.Fatalf("edits = %d, want the 3 made since the last poll: %+v", len(edits), edits)
	}
	want := []struct{ editor, old, new string }{{"carol", "v3", "v4"}, {"bob", "v2", "v3"}, {"alice", "v1", "v2"}}
	for i, w := range want {
		if edits[i].Editor != w.editor || edits[i].Old != w.old || edits[i].New != w.new {
			t.Errorf("edit %d = %+v, want %+v", i, edits[i], w)
		}
	}
}

func TestNewestEditShownEvenIfTimestampLooksOld(t *testing.T) {
	n := &fakeDiffNotifier{}
	// Clock skew: the revision's time isn't after the last poll, but the
	// description did change, so the newest edit is still shown.
	pollEdit(t, n, []github.Revision{
		rev(editTime(-5), "alice", "rewritten description"),
		rev(editTime(-30), "alice", "original description"),
	}, nil)
	if len(n.changes) != 1 || len(n.changes[0].Edits) != 1 {
		t.Errorf("changes = %+v", n.changes)
	}
}

func TestDeletedRevisionIsMarkedUnknown(t *testing.T) {
	n := &fakeDiffNotifier{}
	pollEdit(t, n, []github.Revision{
		rev(editTime(1), "alice", "rewritten description"),
		{EditedAt: editTime(-30), Editor: "alice", HasBody: false},
	}, nil)
	if len(n.changes) != 1 || n.changes[0].Edits[0].Known {
		t.Errorf("an edit whose previous text was deleted must be Known=false: %+v", n.changes)
	}
}

func TestHistoryProblemsStillNotifyWithoutDiff(t *testing.T) {
	cases := map[string]struct {
		revs []github.Revision
		err  error
	}{
		"history request fails":     {err: fmt.Errorf("boom")},
		"history empty":             {},
		"only the original present": {revs: []github.Revision{rev(editTime(-30), "alice", "original description")}},
	}
	for name, tc := range cases {
		n := &fakeDiffNotifier{}
		before := time.Now().UTC().Add(-time.Hour).Truncate(time.Second)
		issue := testIssue(1, before, 0, "open")
		issue.Body = "original description"
		gh := &fakeGitHub{issues: []github.Issue{issue}, revisions: tc.revs, historyErr: tc.err}
		st := state.New()
		w := &Watcher{Client: gh, Notifier: n, Query: "q"}
		if err := w.Poll(context.Background(), st, true); err != nil {
			t.Fatal(err)
		}
		changed := testIssue(1, before.Add(time.Minute), 0, "open")
		changed.Body = "rewritten description"
		gh.issues = []github.Issue{changed}
		if err := w.Poll(context.Background(), st, false); err != nil {
			t.Fatalf("%s: poll must not fail: %v", name, err)
		}
		if len(n.sent) != 1 || !strings.Contains(n.sent[0].message, "description edited") {
			t.Errorf("%s: want one plain notification, got %+v", name, n.sent)
		}
		if len(n.changes) != 0 {
			t.Errorf("%s: no diff should be offered, got %+v", name, n.changes)
		}
	}
}

func TestNoHistoryRequestUnlessADiffCanBeShown(t *testing.T) {
	// A label-only edit never needs the history.
	n := &fakeDiffNotifier{}
	gh := pollEdit(t, n, []github.Revision{rev(editTime(1), "a", "x"), rev(editTime(-1), "a", "y")}, func(i *github.Issue) {
		i.Body = "original description"
		i.Labels = []github.Label{{Name: "triage"}}
	})
	if gh.historyCalls != 0 || len(n.changes) != 0 {
		t.Errorf("label edit: historyCalls=%d changes=%d, want 0 and 0", gh.historyCalls, len(n.changes))
	}

	// A notifier without diff support doesn't need it either.
	plain := &fakeNotifier{}
	gh = pollEdit(t, plain, []github.Revision{rev(editTime(1), "a", "x"), rev(editTime(-1), "a", "y")}, nil)
	if gh.historyCalls != 0 {
		t.Errorf("plain notifier: historyCalls = %d, want 0", gh.historyCalls)
	}
	if len(plain.sent) != 1 || !strings.Contains(plain.sent[0].message, "description edited") {
		t.Errorf("plain notifier sent %+v", plain.sent)
	}
}
