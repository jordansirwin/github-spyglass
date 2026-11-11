// github-spyglass watches GitHub issues matching a search filter and sends
// macOS notifications when they are updated or commented on.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jirwin/github-spyglass/internal/config"
	"github.com/jirwin/github-spyglass/internal/github"
	"github.com/jirwin/github-spyglass/internal/notify"
	"github.com/jirwin/github-spyglass/internal/state"
	"github.com/jirwin/github-spyglass/internal/watcher"
)

func main() {
	query := flag.String("query", "", "GitHub search filter (required), e.g. 'repo:owner/name is:issue is:open label:team'")
	interval := flag.Duration("interval", 5*time.Minute, "poll interval")
	statePath := flag.String("state", state.DefaultPath(), "path to the state file")
	token := flag.String("token", "", "GitHub token (default: the menu bar app's saved token, $GITHUB_TOKEN, $GH_TOKEN, or 'gh auth token')")
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

	explicit := *token
	if explicit == "" {
		explicit = config.LoadToken(config.DefaultTokenPath()) // saved by the menu bar app
	}
	tok := github.ResolveToken(explicit)
	if tok == "" {
		log.Println("warning: no GitHub token found; running unauthenticated (10 searches/min limit, no private repos)")
	}

	st, firstRun, err := state.Load(*statePath)
	if err != nil {
		log.Fatalf("loading state: %v", err)
	}
	if firstRun {
		log.Printf("first run: seeding state from current results, no notifications until the next poll")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	w := &watcher.Watcher{
		Client:   github.New(tok),
		Notifier: notify.New(),
		Query:    *query,
		Verbose:  *verbose,
	}

	log.Printf("watching filter %q every %s", *query, *interval)
	pollErr := w.Poll(ctx, st, firstRun)
	if pollErr != nil {
		log.Printf("poll failed: %v", pollErr)
	}
	if err := state.Save(*statePath, st); err != nil {
		log.Fatalf("saving state: %v", err)
	}
	if *once {
		if pollErr != nil {
			os.Exit(1)
		}
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
			if err := w.Poll(ctx, st, false); err != nil {
				log.Printf("poll failed: %v", err)
				continue
			}
			if err := state.Save(*statePath, st); err != nil {
				log.Printf("saving state: %v", err)
			}
		}
	}
}
