// Package watcher polls GitHub for issues matching a search filter, diffs
// the results against persisted state, and notifies about changes.
package watcher

import (
	"context"
	"log"
	"strings"
	"time"

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
}

// Notifier delivers a user-facing notification. A non-empty url is opened
// when the notification is clicked.
type Notifier interface {
	Notify(title, subtitle, message, url string) error
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
	notify := func(title, subtitle, message, url string) {
		if seedOnly || sent >= maxNotificationsPerPoll {
			return
		}
		sent++
		if sent == maxNotificationsPerPoll {
			message = "(more updates suppressed this poll)"
		}
		if err := w.Notifier.Notify(title, subtitle, message, url); err != nil {
			log.Printf("notification failed: %v", err)
		}
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

			if !reported {
				notify("Updated: "+key, truncate(issue.Title, 60),
					"Issue was edited, labeled, or otherwise changed", issue.HTMLURL)
			}
		}

		st.Issues[key] = cur
	}

	st.Prune(now)
	return nil
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
