// Package github is a minimal GitHub REST API client covering issue search
// and issue comments.
package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

const apiBase = "https://api.github.com"

// maxSearchPages caps pagination of search results (100 per page).
const maxSearchPages = 3

// ErrUnauthorized means GitHub rejected the request's credentials (HTTP 401),
// or none were available.
var ErrUnauthorized = errors.New("GitHub token is invalid or missing")

// ErrTransient marks failures likely to clear on their own: network errors,
// timeouts and 502/503/504 responses.
var ErrTransient = errors.New("temporary network problem")

// requestAttempts is how many times a request is tried before giving up on a
// transient failure.
const requestAttempts = 3

type Client struct {
	http    *http.Client
	mu      sync.RWMutex
	token   string
	baseURL string // apiBase, overridden in tests
	// retryDelay is the pause before the first retry, doubling after that.
	retryDelay time.Duration
}

func New(token string) *Client {
	return &Client{
		http:       &http.Client{Timeout: 20 * time.Second},
		token:      token,
		baseURL:    apiBase,
		retryDelay: time.Second,
	}
}

// SetToken replaces the token used for subsequent requests.
func (c *Client) SetToken(token string) {
	c.mu.Lock()
	c.token = token
	c.mu.Unlock()
}

// Revision is one saved version of an issue description, from GitHub's own
// edit history (the data behind the "edited" dropdown on an issue).
type Revision struct {
	EditedAt time.Time
	Editor   string // login, "" when unknown (e.g. a deleted account)
	// Body is the description text as of this revision. HasBody is false when
	// GitHub no longer has it because the revision history was deleted.
	Body    string
	HasBody bool
}

const descriptionHistoryQuery = `
query($owner: String!, $name: String!, $number: Int!, $n: Int!) {
  repository(owner: $owner, name: $name) {
    issue(number: $number) {
      userContentEdits(first: $n) {
        nodes { editedAt deletedAt diff editor { login } }
      }
    }
  }
}`

// DescriptionHistory returns the issue's description revisions, newest first.
// The newest equals the current description and the oldest is the original
// text, so n revisions describe n-1 edits. An issue that was never edited has
// none. At most limit revisions are returned.
//
// The history comes from the GraphQL API: each node's "diff" field holds the
// full description as of that revision rather than a patch, so callers
// compare consecutive revisions themselves.
func (c *Client) DescriptionHistory(ctx context.Context, issue Issue, limit int) ([]Revision, error) {
	owner, name, ok := strings.Cut(issue.Repo(), "/")
	if !ok || owner == "" || name == "" {
		return nil, fmt.Errorf("cannot determine the repository of %s", issue.Key())
	}
	var resp struct {
		Data struct {
			Repository struct {
				Issue struct {
					UserContentEdits struct {
						Nodes []struct {
							EditedAt  time.Time  `json:"editedAt"`
							DeletedAt *time.Time `json:"deletedAt"`
							Diff      *string    `json:"diff"`
							Editor    *User      `json:"editor"`
						} `json:"nodes"`
					} `json:"userContentEdits"`
				} `json:"issue"`
			} `json:"repository"`
		} `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	err := c.request(ctx, http.MethodPost, c.baseURL+"/graphql", map[string]any{
		"query": descriptionHistoryQuery,
		"variables": map[string]any{
			"owner": owner, "name": name, "number": issue.Number, "n": limit,
		},
	}, &resp)
	if err != nil {
		return nil, err
	}
	if len(resp.Errors) > 0 {
		return nil, fmt.Errorf("GitHub GraphQL: %s", resp.Errors[0].Message)
	}
	nodes := resp.Data.Repository.Issue.UserContentEdits.Nodes
	revs := make([]Revision, 0, len(nodes))
	for _, n := range nodes {
		r := Revision{EditedAt: n.EditedAt, HasBody: n.Diff != nil && n.DeletedAt == nil}
		if r.HasBody {
			r.Body = *n.Diff
		}
		if n.Editor != nil {
			r.Editor = n.Editor.Login
		}
		revs = append(revs, r)
	}
	return revs, nil
}

// transient marks err as ErrTransient unless the caller's context ended,
// which is a cancellation or deadline the caller asked for.
func transient(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return err
	}
	return fmt.Errorf("%w: %w", ErrTransient, err)
}

// CheckAuth verifies the client's token by fetching the authenticated user.
// It returns ErrUnauthorized when GitHub rejects the token.
func (c *Client) CheckAuth(ctx context.Context) error {
	var u User
	if err := c.get(ctx, c.baseURL+"/user", &u); err != nil {
		return err
	}
	if u.Login == "" {
		return ErrUnauthorized
	}
	return nil
}

// ResolveToken returns the first available GitHub token: the explicit one,
// $GITHUB_TOKEN, $GH_TOKEN, or the gh CLI's stored token. Empty when none.
func ResolveToken(explicit string) string {
	if explicit != "" {
		return explicit
	}
	for _, env := range []string{"GITHUB_TOKEN", "GH_TOKEN"} {
		if v := os.Getenv(env); v != "" {
			return v
		}
	}
	if gh := findGH(); gh != "" {
		out, err := exec.Command(gh, "auth", "token").Output()
		if err == nil {
			return strings.TrimSpace(string(out))
		}
		log.Printf("gh auth token failed: %v", err)
	} else {
		log.Printf("gh CLI not found")
	}
	return ""
}

// findGH locates the gh CLI. Apps launched from Finder or at login get a
// minimal PATH without Homebrew, so well-known install locations are tried
// after PATH.
func findGH() string {
	if gh, err := exec.LookPath("gh"); err == nil {
		return gh
	}
	for _, p := range []string{"/opt/homebrew/bin/gh", "/usr/local/bin/gh"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

// User is the author of an issue or comment.
type User struct {
	Login string `json:"login"`
}

// Label is an issue label.
type Label struct {
	Name string `json:"name"`
}

// Milestone is an issue milestone.
type Milestone struct {
	Title string `json:"title"`
}

// Issue is a search result item. The search endpoint returns both issues and
// pull requests; add "is:issue" to the filter to exclude PRs.
type Issue struct {
	Number        int        `json:"number"`
	Title         string     `json:"title"`
	Body          string     `json:"body"`
	HTMLURL       string     `json:"html_url"`
	UpdatedAt     time.Time  `json:"updated_at"`
	Comments      int        `json:"comments"`
	State         string     `json:"state"`
	RepositoryURL string     `json:"repository_url"`
	User          User       `json:"user"`
	Labels        []Label    `json:"labels"`
	Assignees     []User     `json:"assignees"`
	Milestone     *Milestone `json:"milestone"`
}

// LabelNames returns the issue's label names. Never nil.
func (i Issue) LabelNames() []string {
	names := make([]string, 0, len(i.Labels))
	for _, l := range i.Labels {
		names = append(names, l.Name)
	}
	return names
}

// AssigneeLogins returns the issue's assignee logins. Never nil.
func (i Issue) AssigneeLogins() []string {
	logins := make([]string, 0, len(i.Assignees))
	for _, u := range i.Assignees {
		logins = append(logins, u.Login)
	}
	return logins
}

// MilestoneTitle returns the milestone title, or "" when there is none.
func (i Issue) MilestoneTitle() string {
	if i.Milestone == nil {
		return ""
	}
	return i.Milestone.Title
}

// Repo returns "owner/name", derived from the API repository URL.
func (i Issue) Repo() string {
	if idx := strings.Index(i.RepositoryURL, "/repos/"); idx >= 0 {
		return i.RepositoryURL[idx+len("/repos/"):]
	}
	return ""
}

// Key uniquely identifies an issue as "owner/name#number".
func (i Issue) Key() string {
	return fmt.Sprintf("%s#%d", i.Repo(), i.Number)
}

type Comment struct {
	HTMLURL   string    `json:"html_url"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	User      User      `json:"user"`
}

// SearchIssues returns all issues matching the search filter, paginating up
// to maxSearchPages pages sorted by most recently updated.
func (c *Client) SearchIssues(ctx context.Context, query string) ([]Issue, error) {
	var all []Issue
	for page := 1; page <= maxSearchPages; page++ {
		q := url.Values{
			"q":               {query},
			"sort":            {"updated"},
			"order":           {"desc"},
			"per_page":        {"100"},
			"page":            {fmt.Sprint(page)},
			"advanced_search": {"true"},
		}
		var result struct {
			TotalCount int     `json:"total_count"`
			Items      []Issue `json:"items"`
		}
		if err := c.get(ctx, c.baseURL+"/search/issues?"+q.Encode(), &result); err != nil {
			return nil, fmt.Errorf("searching issues: %w", err)
		}
		all = append(all, result.Items...)
		if len(all) >= result.TotalCount || len(result.Items) < 100 {
			break
		}
	}
	return all, nil
}

// CommentsSince returns comments on the issue created at or after since.
func (c *Client) CommentsSince(ctx context.Context, issue Issue, since time.Time) ([]Comment, error) {
	u := fmt.Sprintf("%s/repos/%s/issues/%d/comments?per_page=100&since=%s",
		c.baseURL, issue.Repo(), issue.Number, url.QueryEscape(since.UTC().Format(time.RFC3339)))
	var comments []Comment
	if err := c.get(ctx, u, &comments); err != nil {
		return nil, err
	}
	// The since parameter filters on updated_at, so an edit to an old comment
	// can show up here; keep only genuinely new comments.
	fresh := comments[:0]
	for _, cm := range comments {
		if !cm.CreatedAt.Before(since) {
			fresh = append(fresh, cm)
		}
	}
	return fresh, nil
}

// get fetches rawURL into out, retrying transient failures with backoff.
func (c *Client) get(ctx context.Context, rawURL string, out any) error {
	return c.request(ctx, http.MethodGet, rawURL, nil, out)
}

// request sends one API request, retrying transient failures with backoff.
// payload, when non-nil, is sent as the JSON body. Only idempotent requests
// (GETs and read-only GraphQL queries) go through here.
func (c *Client) request(ctx context.Context, method, rawURL string, payload any, out any) error {
	var body []byte
	if payload != nil {
		var err error
		if body, err = json.Marshal(payload); err != nil {
			return err
		}
	}
	delay := c.retryDelay
	var err error
	for attempt := 1; ; attempt++ {
		err = c.requestOnce(ctx, method, rawURL, body, out)
		if err == nil || !errors.Is(err, ErrTransient) || attempt == requestAttempts {
			return err
		}
		select {
		case <-ctx.Done():
			return err
		case <-time.After(delay):
		}
		delay *= 2
	}
}

func (c *Client) requestOnce(ctx context.Context, method, rawURL string, payload []byte, out any) error {
	var reader io.Reader
	if payload != nil {
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "github-spyglass")
	c.mu.RLock()
	token := c.token
	c.mu.RUnlock()
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return transient(ctx, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return transient(ctx, err)
	}
	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout:
			return fmt.Errorf("%w: GitHub API %s", ErrTransient, resp.Status)
		}
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			if sec, perr := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); perr == nil {
				return fmt.Errorf("GitHub rate limit exceeded (resets at %s)",
					time.Unix(sec, 0).Local().Format("15:04:05"))
			}
			return fmt.Errorf("GitHub rate limit exceeded")
		}
		// With no token at all, a private repo shows up as 404/422 rather than 401.
		noTokenDenied := token == "" &&
			(resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusUnprocessableEntity)
		if resp.StatusCode == http.StatusUnauthorized || noTokenDenied {
			return fmt.Errorf("%w (GitHub API %s)", ErrUnauthorized, resp.Status)
		}
		return fmt.Errorf("GitHub API %s: %s: %s", resp.Status, rawURL, msg)
	}
	return json.Unmarshal(body, out)
}
