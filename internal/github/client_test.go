package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// testClient returns a Client pointed at a test server.
func testClient(t *testing.T, handler http.Handler) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("test-token")
	c.baseURL = srv.URL
	return c
}

func TestIssueHelpers(t *testing.T) {
	issue := Issue{
		Number:        42,
		RepositoryURL: "https://api.github.com/repos/owner/repo",
		Labels:        []Label{{Name: "bug"}, {Name: "triage"}},
		Assignees:     []User{{Login: "alice"}},
	}
	if got := issue.Repo(); got != "owner/repo" {
		t.Errorf("Repo() = %q, want owner/repo", got)
	}
	if got := issue.Key(); got != "owner/repo#42" {
		t.Errorf("Key() = %q, want owner/repo#42", got)
	}
	if got := issue.LabelNames(); len(got) != 2 || got[0] != "bug" || got[1] != "triage" {
		t.Errorf("LabelNames() = %v", got)
	}
	if got := issue.AssigneeLogins(); len(got) != 1 || got[0] != "alice" {
		t.Errorf("AssigneeLogins() = %v", got)
	}
	if got := issue.MilestoneTitle(); got != "" {
		t.Errorf("MilestoneTitle() with nil milestone = %q, want empty", got)
	}
	issue.Milestone = &Milestone{Title: "v2"}
	if got := issue.MilestoneTitle(); got != "v2" {
		t.Errorf("MilestoneTitle() = %q, want v2", got)
	}

	// Helpers never return nil even with no labels/assignees.
	empty := Issue{}
	if empty.LabelNames() == nil || empty.AssigneeLogins() == nil {
		t.Error("LabelNames/AssigneeLogins returned nil for empty issue")
	}
	if got := empty.Repo(); got != "" {
		t.Errorf("Repo() with no repository URL = %q, want empty", got)
	}
}

func TestSearchIssuesSinglePage(t *testing.T) {
	var gotReq *http.Request
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReq = r.Clone(context.Background())
		json.NewEncoder(w).Encode(map[string]any{
			"total_count": 2,
			"items": []Issue{
				{Number: 1, Title: "first"},
				{Number: 2, Title: "second"},
			},
		})
	}))

	issues, err := c.SearchIssues(context.Background(), "repo:owner/repo is:issue")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Fatalf("got %d issues, want 2", len(issues))
	}

	q := gotReq.URL.Query()
	if q.Get("q") != "repo:owner/repo is:issue" {
		t.Errorf("query param q = %q", q.Get("q"))
	}
	if q.Get("sort") != "updated" || q.Get("advanced_search") != "true" {
		t.Errorf("missing search params in %v", q)
	}
	if got := gotReq.Header.Get("Authorization"); got != "Bearer test-token" {
		t.Errorf("Authorization = %q, want Bearer test-token", got)
	}
	if got := gotReq.Header.Get("X-GitHub-Api-Version"); got == "" {
		t.Error("missing X-GitHub-Api-Version header")
	}
}

func TestSearchIssuesPaginates(t *testing.T) {
	const total = 150
	var pages []string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		pages = append(pages, page)
		n := 100
		if page == "2" {
			n = 50
		}
		items := make([]Issue, n)
		for i := range items {
			items[i] = Issue{Number: i}
		}
		json.NewEncoder(w).Encode(map[string]any{"total_count": total, "items": items})
	}))

	issues, err := c.SearchIssues(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != total {
		t.Fatalf("got %d issues, want %d", len(issues), total)
	}
	if len(pages) != 2 || pages[0] != "1" || pages[1] != "2" {
		t.Errorf("fetched pages %v, want [1 2]", pages)
	}
}

func TestSearchIssuesStopsAtPageCap(t *testing.T) {
	requests := 0
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		items := make([]Issue, 100)
		json.NewEncoder(w).Encode(map[string]any{"total_count": 10000, "items": items})
	}))

	issues, err := c.SearchIssues(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	if requests != maxSearchPages {
		t.Errorf("made %d requests, want cap of %d", requests, maxSearchPages)
	}
	if len(issues) != maxSearchPages*100 {
		t.Errorf("got %d issues, want %d", len(issues), maxSearchPages*100)
	}
}

func TestRateLimitError(t *testing.T) {
	reset := time.Now().Add(time.Minute)
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", fmt.Sprint(reset.Unix()))
		w.WriteHeader(http.StatusForbidden)
	}))

	_, err := c.SearchIssues(context.Background(), "q")
	if err == nil {
		t.Fatal("want rate limit error, got nil")
	}
	if !strings.Contains(err.Error(), "rate limit exceeded") {
		t.Errorf("error = %q, want it to mention the rate limit", err)
	}
	if !strings.Contains(err.Error(), reset.Local().Format("15:04:05")) {
		t.Errorf("error = %q, want the reset time %s", err, reset.Local().Format("15:04:05"))
	}
}

func TestAPIErrorIncludesStatusAndBody(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message": "Validation Failed"}`)
	}))

	_, err := c.SearchIssues(context.Background(), "bad query")
	if err == nil {
		t.Fatal("want error, got nil")
	}
	if !strings.Contains(err.Error(), "422") || !strings.Contains(err.Error(), "Validation Failed") {
		t.Errorf("error = %q, want status and body", err)
	}
}

func TestCommentsSinceFiltersEditedComments(t *testing.T) {
	since := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	var gotPath, gotSince string
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotSince = r.URL.Query().Get("since")
		// The API's since filters on updated_at: an old comment edited
		// recently is returned too, and must be dropped client-side.
		json.NewEncoder(w).Encode([]Comment{
			{Body: "old comment, recently edited", CreatedAt: since.Add(-time.Hour)},
			{Body: "genuinely new", CreatedAt: since.Add(time.Minute)},
			{Body: "created exactly at since", CreatedAt: since},
		})
	}))

	issue := Issue{Number: 7, RepositoryURL: "https://api.github.com/repos/owner/repo"}
	comments, err := c.CommentsSince(context.Background(), issue, since)
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/repos/owner/repo/issues/7/comments" {
		t.Errorf("path = %q", gotPath)
	}
	if gotSince != since.Format(time.RFC3339) {
		t.Errorf("since param = %q, want %q", gotSince, since.Format(time.RFC3339))
	}
	if len(comments) != 2 {
		t.Fatalf("got %d comments, want 2 (edited old comment dropped): %+v", len(comments), comments)
	}
	if comments[0].Body != "genuinely new" || comments[1].Body != "created exactly at since" {
		t.Errorf("wrong comments kept: %+v", comments)
	}
}

func TestUnauthorizedIsErrUnauthorized(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		fmt.Fprint(w, `{"message": "Bad credentials"}`)
	}))
	if _, err := c.SearchIssues(context.Background(), "q"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("error = %v, want ErrUnauthorized", err)
	}
}

func TestNoTokenDeniedIsErrUnauthorized(t *testing.T) {
	c := testClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		fmt.Fprint(w, `{"message": "Validation Failed"}`)
	}))
	c.SetToken("")
	if _, err := c.SearchIssues(context.Background(), "q"); !errors.Is(err, ErrUnauthorized) {
		t.Errorf("without a token, 422 = %v, want ErrUnauthorized", err)
	}
	// With a token, a 422 is a real query error, not an auth problem.
	c.SetToken("tok")
	if _, err := c.SearchIssues(context.Background(), "q"); errors.Is(err, ErrUnauthorized) {
		t.Errorf("with a token, 422 should not be ErrUnauthorized: %v", err)
	}
}
