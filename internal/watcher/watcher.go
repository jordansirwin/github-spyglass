// Package watcher polls GitHub for issues matching a search filter, diffs
// the results against persisted state, and notifies about changes.
package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/jirwin/github-spyglass/internal/diff"
	"github.com/jirwin/github-spyglass/internal/github"
	"github.com/jirwin/github-spyglass/internal/state"
)

// maxNotificationsPerPoll caps how many notifications a single poll can fire
// so a noisy filter doesn't flood Notification Center.
const maxNotificationsPerPoll = 10

// GitHub is the subset of the GitHub API the watcher needs.
type GitHub interface {
	SearchIssues(ctx context.Context, query string) ([]github.Issue, error)
	CommentsSince(ctx context.Context, issue github.Issue, since time.Time) ([]github.Comment, error)
	// DescriptionHistory returns the issue's description revisions, newest first.
	DescriptionHistory(ctx context.Context, issue github.Issue, limit int) ([]github.Revision, error)
}

// Notifier delivers a user-facing notification. A non-empty url is opened
// when the notification is clicked.
type Notifier interface {
	Notify(title, subtitle, message, url string) error
}

// DiffNotifier is a Notifier that can attach a "view diff" action to a
// notification about an edited description. The watcher uses it when the
// notifier implements it and falls back to Notify otherwise.
type DiffNotifier interface {
	Notifier
	NotifyDescriptionChange(title, subtitle, message, url string, change diff.Change) error
}

type Watcher struct {
	Client   GitHub
	Notifier Notifier
	Query    string
	Verbose  bool
}

// Poll runs one polling pass: search, diff against state, notify, and update
// state in place. With seedOnly set, state is recorded but nothing is
// notified (used on first run so existing issues don't alert).
func (w *Watcher) Poll(ctx context.Context, st *state.State, seedOnly bool) error {
	issues, err := w.Client.SearchIssues(ctx, w.Query)
	if err != nil {
		return err
	}
	if w.Verbose {
		log.Printf("poll: %d issues match filter", len(issues))
	}

	now := time.Now().UTC()
	sent := 0
	// send delivers one notification, with a diff action when change is set
	// and the notifier supports it.
	send := func(title, subtitle, message, url string, change *diff.Change) {
		if seedOnly || sent >= maxNotificationsPerPoll {
			return
		}
		sent++
		if sent == maxNotificationsPerPoll {
			message = "(more updates suppressed this poll)"
			change = nil
		}
		var err error
		if dn, ok := w.Notifier.(DiffNotifier); ok && change != nil {
			err = dn.NotifyDescriptionChange(title, subtitle, message, url, *change)
		} else {
			err = w.Notifier.Notify(title, subtitle, message, url)
		}
		if err != nil {
			log.Printf("notification failed: %v", err)
		}
	}
	notify := func(title, subtitle, message, url string) {
		send(title, subtitle, message, url, nil)
	}

	for _, issue := range issues {
		key := issue.Key()
		prev, seen := st.Issues[key]
		cur := state.IssueState{
			UpdatedAt: issue.UpdatedAt,
			Comments:  issue.Comments,
			State:     issue.State,
			Title:     issue.Title,
			LastSeen:  now,
			Labels:    issue.LabelNames(),
			Assignees: issue.AssigneeLogins(),
			Milestone: issue.MilestoneTitle(),
			BodyHash:  hashBody(issue.Body),
		}

		switch {
		case !seen:
			if !seedOnly {
				log.Printf("new match: %s %q", key, issue.Title)
				notify("New issue matches filter", key, issue.Title, issue.HTMLURL)
			}

		case issue.UpdatedAt.After(prev.UpdatedAt):
			log.Printf("update: %s %q (%s -> %s)", key, issue.Title,
				prev.UpdatedAt.Format(time.RFC3339), issue.UpdatedAt.Format(time.RFC3339))
			reported := false

			if issue.Comments > prev.Comments && !seedOnly {
				comments, err := w.Client.CommentsSince(ctx, issue, prev.UpdatedAt.Add(time.Second))
				if err != nil {
					log.Printf("fetching comments for %s: %v", key, err)
				}
				for _, c := range comments {
					notify("New comment on "+key, truncate(issue.Title, 60),
						c.User.Login+": "+condense(c.Body, 200), c.HTMLURL)
					reported = true
				}
			}

			if issue.State != prev.State {
				verb := issue.State
				if issue.State == "open" && prev.State == "closed" {
					verb = "reopened"
				}
				notify(capitalize(verb)+": "+key, truncate(issue.Title, 60),
					"State changed from "+prev.State+" to "+issue.State, issue.HTMLURL)
				reported = true
			}

			if changes := describeChanges(prev, issue); len(changes) > 0 {
				var change *diff.Change
				if descriptionEdited(prev, issue) {
					change = w.descriptionChange(ctx, key, issue, prev)
				}
				send("Updated: "+key, truncate(issue.Title, 60),
					condense(strings.Join(changes, ", "), 200), issue.HTMLURL, change)
			} else if !reported {
				notify("Updated: "+key, truncate(issue.Title, 60),
					"Issue was edited, labeled, or otherwise changed", issue.HTMLURL)
			}
		}

		st.Issues[key] = cur
	}

	st.Prune(now)
	return nil
}

// describeChanges compares the issue against its previous snapshot and
// returns human-readable descriptions of what changed. State (open/closed)
// and comments are handled separately by Poll.
func describeChanges(prev state.IssueState, issue github.Issue) []string {
	var changes []string
	if prev.Title != "" && prev.Title != issue.Title {
		changes = append(changes, fmt.Sprintf("renamed to %q", issue.Title))
	}
	if prev.BodyHash == "" {
		// Entry predates snapshot fields; nothing more to diff against.
		return changes
	}
	if added, removed := diffSet(prev.Labels, issue.LabelNames()); len(added)+len(removed) > 0 {
		changes = append(changes, "labels "+joinSigned(added, removed))
	}
	if added, removed := diffSet(prev.Assignees, issue.AssigneeLogins()); len(added)+len(removed) > 0 {
		changes = append(changes, "assignees "+joinSigned(added, removed))
	}
	if cur := issue.MilestoneTitle(); cur != prev.Milestone {
		if cur == "" {
			changes = append(changes, "milestone removed")
		} else {
			changes = append(changes, "milestone set to "+cur)
		}
	}
	if descriptionEdited(prev, issue) {
		changes = append(changes, "description edited")
	}
	return changes
}

// maxDiffEdits caps how many edits one diff page shows.
const maxDiffEdits = 10

// descriptionChange builds the diff for an edited description from GitHub's
// own revision history, covering every edit made since the issue was last
// seen (at least the newest). It returns nil when the notifier can't show
// diffs or the history is unavailable, in which case the notification goes
// out without a diff button.
func (w *Watcher) descriptionChange(ctx context.Context, key string, issue github.Issue, prev state.IssueState) *diff.Change {
	if _, ok := w.Notifier.(DiffNotifier); !ok {
		return nil
	}
	revs, err := w.Client.DescriptionHistory(ctx, issue, maxDiffEdits+1)
	if err != nil {
		log.Printf("fetching description history for %s: %v", key, err)
		return nil
	}
	// n revisions describe n-1 edits: each edit turned revs[k+1] into revs[k].
	var edits []diff.Edit
	for k := 0; k+1 < len(revs); k++ {
		r, before := revs[k], revs[k+1]
		if len(edits) > 0 && !r.EditedAt.After(prev.UpdatedAt) {
			break // older than what we last saw
		}
		edits = append(edits, diff.Edit{
			When:   r.EditedAt,
			Editor: r.Editor,
			Old:    before.Body,
			New:    r.Body,
			Known:  r.HasBody && before.HasBody,
		})
	}
	if len(edits) == 0 {
		return nil
	}
	return &diff.Change{Key: key, Title: issue.Title, URL: issue.HTMLURL, Edits: edits}
}

// descriptionEdited reports whether the issue's description differs from the
// snapshot. Entries without a snapshot (empty BodyHash) never count.
func descriptionEdited(prev state.IssueState, issue github.Issue) bool {
	return prev.BodyHash != "" && hashBody(issue.Body) != prev.BodyHash
}

// diffSet returns the elements added to and removed from old, in input order.
func diffSet(old, cur []string) (added, removed []string) {
	oldSet := make(map[string]bool, len(old))
	for _, s := range old {
		oldSet[s] = true
	}
	curSet := make(map[string]bool, len(cur))
	for _, s := range cur {
		curSet[s] = true
		if !oldSet[s] {
			added = append(added, s)
		}
	}
	for _, s := range old {
		if !curSet[s] {
			removed = append(removed, s)
		}
	}
	return added, removed
}

// joinSigned renders additions and removals as "+a +b -c".
func joinSigned(added, removed []string) string {
	parts := make([]string, 0, len(added)+len(removed))
	for _, s := range added {
		parts = append(parts, "+"+s)
	}
	for _, s := range removed {
		parts = append(parts, "-"+s)
	}
	return strings.Join(parts, " ")
}

func hashBody(body string) string {
	sum := sha256.Sum256([]byte(body))
	return hex.EncodeToString(sum[:8])
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// condense collapses all whitespace (including newlines) into single spaces
// so as much of the comment as possible fits in the notification body.
func condense(s string, max int) string {
	return truncate(strings.Join(strings.Fields(s), " "), max)
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
