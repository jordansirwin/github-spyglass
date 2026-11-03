// Package github is a minimal GitHub REST API client covering issue search
// and issue comments.
package github

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const apiBase = "https://api.github.com"

// maxSearchPages caps pagination of search results (100 per page).
const maxSearchPages = 3

type Client struct {
	http  *http.Client
	token string
}

func New(token string) *Client {
	return &Client{
		http:  &http.Client{Timeout: 30 * time.Second},
		token: token,
	}
}

// User is the author of an issue or comment.
type User struct {
	Login string `json:"login"`
}

// Issue is a search result item. The search endpoint returns both issues and
// pull requests; add "is:issue" to the filter to exclude PRs.
type Issue struct {
	Number        int       `json:"number"`
	Title         string    `json:"title"`
	HTMLURL       string    `json:"html_url"`
	UpdatedAt     time.Time `json:"updated_at"`
	Comments      int       `json:"comments"`
	State         string    `json:"state"`
	RepositoryURL string    `json:"repository_url"`
	User          User      `json:"user"`
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
		if err := c.get(ctx, apiBase+"/search/issues?"+q.Encode(), &result); err != nil {
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
		apiBase, issue.Repo(), issue.Number, url.QueryEscape(since.UTC().Format(time.RFC3339)))
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

func (c *Client) get(ctx context.Context, rawURL string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "github-spyglass")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 10<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(body))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		if resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0" {
			return fmt.Errorf("GitHub rate limit exceeded (resets at %s)", resp.Header.Get("X-RateLimit-Reset"))
		}
		return fmt.Errorf("GitHub API %s: %s: %s", resp.Status, rawURL, msg)
	}
	return json.Unmarshal(body, out)
}
