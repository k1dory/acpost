package forge

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// A repeat of an open (a retry after a lost answer) finds the pull request the first one opened,
// on each forge: refused as a duplicate (GitHub 422, GitLab and Forgejo 409), looked up by head.
func TestOpenPullFindsTheOneOpen(t *testing.T) {
	for _, tc := range []struct {
		kind, post, list string
		status           int
		open             any
	}{
		{GitHub, "POST /api/v3/repos/o/r/pulls", "GET /api/v3/repos/o/r/pulls", 422,
			[]map[string]any{{"number": 7, "state": "open", "html_url": "u7", "head": map[string]any{"ref": "feat", "repo": map[string]string{"full_name": "o/r"}}, "base": map[string]string{"ref": "main"}}}},
		{Forgejo, "POST /api/v1/repos/o/r/pulls", "GET /api/v1/repos/o/r/pulls", 409,
			[]map[string]any{
				{"number": 6, "state": "open", "html_url": "u6", "head": map[string]any{"ref": "other", "repo": map[string]string{"full_name": "o/r"}}, "base": map[string]string{"ref": "main"}},
				{"number": 7, "state": "open", "html_url": "u7", "head": map[string]any{"ref": "feat", "repo": map[string]string{"full_name": "o/r"}}, "base": map[string]string{"ref": "main"}}}},
		{GitLab, "POST /api/v4/projects/o%2Fr/merge_requests", "GET /api/v4/projects/o%2Fr/merge_requests", 409,
			[]map[string]any{{"iid": 7, "state": "opened", "web_url": "u7", "source_branch": "feat", "target_branch": "main", "source_project_id": 1, "target_project_id": 1}}},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			var query string
			c, host, _ := fake(t, tc.kind, map[string]any{
				tc.post: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.WriteHeader(tc.status)
					w.Write([]byte(`{"message":"A pull request already exists for o:feat."}`))
				}),
				tc.list: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					query = r.URL.RawQuery
					json.NewEncoder(w).Encode(tc.open)
				}),
			})
			srv := Server{Kind: tc.kind, API: "https://" + host + map[string]string{GitHub: "/api/v3", Forgejo: "/api/v1", GitLab: "/api/v4"}[tc.kind]}
			p, err := c.OpenPull(ctx, srv, Repo{Host: host, Path: "o/r"}, PullRequest{Head: "feat", Base: "main", Title: "t"})
			if err != nil || p.Number != 7 || p.URL != "u7" || !p.Existing {
				t.Fatalf("a repeated open: %+v %v", p, err)
			}
			if tc.kind == GitHub && !strings.Contains(query, "head=o%3Afeat") {
				t.Fatalf("looked up with %q", query)
			}
		})
	}
	// Refused for another reason: the error, as it was.
	c, host, _ := fake(t, GitHub, map[string]any{"POST /api/v3/repos/o/r/pulls": 403})
	if _, err := c.OpenPull(ctx, Server{Kind: GitHub, API: "https://" + host + "/api/v3"}, Repo{Host: host, Path: "o/r"},
		PullRequest{Head: "feat", Base: "main", Title: "t"}); err == nil {
		t.Fatal("a refusal opened a pull request")
	}
}

// pagedPulls serves n pull requests a page at a time (per_page/limit, page), with a Link rel="next"
// while there are more when link is set.
func pagedPulls(n int, link bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		size, _ := strconv.Atoi(r.URL.Query().Get("per_page") + r.URL.Query().Get("limit"))
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		page = max(page, 1)
		var out []map[string]any
		for i := (page - 1) * size; i < min(page*size, n); i++ {
			out = append(out, map[string]any{"number": i + 1, "state": "open"})
		}
		if link && page*size < n {
			w.Header().Set("Link", fmt.Sprintf(`<%s?page=%d>; rel="next"`, r.URL.Path, page+1))
		}
		json.NewEncoder(w).Encode(out)
	}
}

// A list longer than a page is read whole (Link rel="next", or full pages when the forge says
// nothing), and one longer than listPages pages says it is cut rather than ending silently.
func TestPullsFollowPages(t *testing.T) {
	for _, link := range []bool{true, false} {
		c, host, _ := fake(t, GitHub, map[string]any{"GET /api/v3/repos/o/r/pulls": pagedPulls(51, link)})
		srv, r := Server{Kind: GitHub, API: "https://" + host + "/api/v3"}, Repo{Host: host, Path: "o/r"}
		l, err := c.PullList(ctx, srv, r, "open")
		if err != nil || len(l.Items) != 51 || l.Truncated || l.Items[50].Number != 51 {
			t.Fatalf("link %v: %d items, truncated %v, %v", link, len(l.Items), l.Truncated, err)
		}
	}
	c, host, _ := fake(t, Forgejo, map[string]any{"GET /api/v1/repos/o/r/pulls": pagedPulls(1000, true)})
	l, err := c.PullList(ctx, Server{Kind: Forgejo, API: "https://" + host + "/api/v1"}, Repo{Host: host, Path: "o/r"}, "open")
	if err != nil || len(l.Items) != listPages*pageLen || !l.Truncated {
		t.Fatalf("a long list: %d items, truncated %v, %v", len(l.Items), l.Truncated, err)
	}
}

// Jobs that cannot be read are said on their run, not dropped as if it had none; more runs than
// read are said too.
func TestChecksSayWhatIsMissing(t *testing.T) {
	c, host, _ := fake(t, GitHub, map[string]any{
		"GET /api/v3/repos/o/r/actions/runs": map[string]any{"total_count": 25, "workflow_runs": []map[string]any{{"id": 5, "name": "ci", "status": "completed", "conclusion": "failure"}}},
		"GET /api/v3/repos/o/r/actions/runs/5/jobs": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(403)
			w.Write([]byte(`{"message":"Resource not accessible by integration"}`))
		}),
	})
	l, err := c.CheckList(ctx, Server{Kind: GitHub, API: "https://" + host + "/api/v3"}, Repo{Host: host, Path: "o/r"}, "main")
	if err != nil || len(l.Items) != 1 || !strings.Contains(l.Items[0].JobsError, "not accessible") || !l.Truncated {
		t.Fatalf("%+v %v", l, err)
	}
	c, host, _ = fake(t, GitLab, map[string]any{
		"GET /api/v4/projects/o%2Fr/pipelines":        []map[string]any{{"id": 9, "status": "failed"}},
		"GET /api/v4/projects/o%2Fr/pipelines/9/jobs": 403,
	})
	l, err = c.CheckList(ctx, Server{Kind: GitLab, API: "https://" + host + "/api/v4"}, Repo{Host: host, Path: "o/r"}, "main")
	if err != nil || len(l.Items) != 1 || l.Items[0].JobsError == "" {
		t.Fatalf("GitLab: %+v %v", l, err)
	}
}
