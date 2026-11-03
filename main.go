// github-spyglass watches GitHub issues matching a search filter and sends
// macOS notifications when they are updated or commented on.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// maxNotificationsPerPoll caps how many notifications a single poll can fire
// so a noisy filter doesn't flood Notification Center.
const maxNotificationsPerPoll = 10

func main() {
	query := flag.String("query", "", "GitHub search filter (required), e.g. 'repo:elastic/cloud is:issue is:open label:team'")
	interval := flag.Duration("interval", 5*time.Minute, "poll interval")
	statePath := flag.String("state", defaultStatePath(), "path to the state file")
	token := flag.String("token", "", "GitHub token (default: $GITHUB_TOKEN, $GH_TOKEN, or 'gh auth token')")
	once := flag.Bool("once", false, "run a single poll and exit")
	verbose := flag.Bool("verbose", false, "verbose logging")
	flag.Parse()

	if *query == "" {
		fmt.Fprintln(os.Stderr, "error: -query is required")
		flag.Usage()
		os.Exit(2)
	}
	if *interval < time.Minute {
		fmt.Fprintln(os.Stderr, "error: -interval must be at least 1m (GitHub search rate limits)")
		os.Exit(2)
	}

	tok := resolveToken(*token)
	if tok == "" {
		log.Println("warning: no GitHub token found; running unauthenticated (10 searches/min limit, no private repos)")
	}

	client := NewGitHubClient(tok)
	notifier := NewNotifier()

	state, firstRun, err := LoadState(*statePath)
	if err != nil {
		log.Fatalf("loading state: %v", err)
	}
	if firstRun {
		log.Printf("first run: seeding state from current results, no notifications until the next poll")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	w := &Watcher{
		client:   client,
		notifier: notifier,
		query:    *query,
		verbose:  *verbose,
	}

	log.Printf("watching filter %q every %s", *query, *interval)
	if err := w.Poll(ctx, state, firstRun); err != nil {
		log.Printf("poll failed: %v", err)
	}
	if err := SaveState(*statePath, state); err != nil {
		log.Fatalf("saving state: %v", err)
	}
	if *once {
		return
	}

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Println("shutting down")
			return
		case <-ticker.C:
			if err := w.Poll(ctx, state, false); err != nil {
				log.Printf("poll failed: %v", err)
				continue
			}
			if err := SaveState(*statePath, state); err != nil {
				log.Printf("saving state: %v", err)
			}
		}
	}
}

// Watcher runs one polling pass: search, diff against state, notify.
type Watcher struct {
	client   *GitHubClient
	notifier *Notifier
	query    string
	verbose  bool
}

func (w *Watcher) Poll(ctx context.Context, state *State, seedOnly bool) error {
	issues, err := w.client.SearchIssues(ctx, w.query)
	if err != nil {
		return err
	}
	if w.verbose {
		log.Printf("poll: %d issues match filter", len(issues))
	}

	now := time.Now().UTC()
	sent := 0
	notify := func(title, subtitle, message, url string) {
		if seedOnly {
			return
		}
		if sent >= maxNotificationsPerPoll {
			return
		}
		sent++
		if sent == maxNotificationsPerPoll {
			message = "(more updates suppressed this poll)"
		}
		if err := w.notifier.Notify(title, subtitle, message, url); err != nil {
			log.Printf("notification failed: %v", err)
		}
	}

	for _, issue := range issues {
		key := issue.Key()
		prev, seen := state.Issues[key]
		cur := IssueState{
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
				comments, err := w.client.CommentsSince(ctx, issue, prev.UpdatedAt.Add(time.Second))
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

		state.Issues[key] = cur
	}

	state.Prune(now)
	return nil
}

func resolveToken(flagToken string) string {
	if flagToken != "" {
		return flagToken
	}
	for _, env := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	if gh, err := exec.LookPath("gh"); err == nil {
		out, err := exec.Command(gh, "auth", "token").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
	}
	return ""
}

func defaultStatePath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "github-spyglass-state.json"
	}
	return filepath.Join(home, ".github-spyglass", "state.json")
}

// condense collapses all whitespace (including newlines) into single spaces
// so as much of the comment as possible fits in the notification body.
func condense(s string, max int) string {
	return truncate(strings.Join(strings.Fields(s), " "), max)
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func truncate(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
