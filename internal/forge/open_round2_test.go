package forge

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func headOf(n int, ref, repo string) map[string]any {
	return map[string]any{"number": n, "state": "open", "html_url": "u" + string(rune('0'+n)),
		"head": map[string]any{"ref": ref, "repo": map[string]string{"full_name": repo}}, "base": map[string]string{"ref": "main"}}
}

// A repeated open finds the pull request of the same source: owner:branch names a fork's branch
// (GitHub looked up by it as given, not owner:owner:branch), and a fork's pull request from a
// branch of the same name is not the base repository's own.
func TestAlreadyOpenKeepsTheSource(t *testing.T) {
	var query string
	dup := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(422) })
	c, host, _ := fake(t, GitHub, map[string]any{
		"POST /api/v3/repos/o/r/pulls": dup,
		"GET /api/v3/repos/o/r/pulls": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			query = r.URL.RawQuery
			json.NewEncoder(w).Encode([]map[string]any{headOf(5, "feat", "alice/r")})
		}),
	})
	srv := Server{Kind: GitHub, API: "https://" + host + "/api/v3"}
	p, err := c.OpenPull(ctx, srv, Repo{Host: host, Path: "o/r"}, PullRequest{Head: "alice:feat", Base: "main", Title: "t"})
	if err != nil || p.Number != 5 || !p.Existing || !strings.Contains(query, "head=alice%3Afeat&") && !strings.HasSuffix(query, "head=alice%3Afeat") {
		t.Fatalf("a fork's head: %+v %v (looked up with %q)", p, err, query)
	}
	// Forgejo lists them all: bob's fork has a feat too.
	c, host, _ = fake(t, Forgejo, map[string]any{
		"POST /api/v1/repos/o/r/pulls": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(409) }),
		"GET /api/v1/repos/o/r/pulls":  []map[string]any{headOf(6, "feat", "bob/r"), headOf(7, "feat", "o/r")},
	})
	srv = Server{Kind: Forgejo, API: "https://" + host + "/api/v1"}
	if p, err := c.OpenPull(ctx, srv, Repo{Host: host, Path: "o/r"}, PullRequest{Head: "feat", Base: "main", Title: "t"}); err != nil || p.Number != 7 {
		t.Fatalf("the base repository's own feat: %+v %v", p, err)
	}
	if p, err := c.OpenPull(ctx, srv, Repo{Host: host, Path: "o/r"}, PullRequest{Head: "bob:feat", Base: "main", Title: "t"}); err != nil || p.Number != 6 {
		t.Fatalf("bob's feat: %+v %v", p, err)
	}
	if _, err := c.OpenPull(ctx, srv, Repo{Host: host, Path: "o/r"}, PullRequest{Head: "carol:feat", Base: "main", Title: "t"}); err == nil {
		t.Fatal("carol's feat found in another fork's pull request")
	}
}

// A lookup that fails (503) is that failure, worth a retry, not the duplicate's refusal; the retry
// finds the pull request.
func TestAlreadyOpenLookupFailureIsItsOwn(t *testing.T) {
	var lists atomic.Int32
	c, host, _ := fake(t, GitHub, map[string]any{
		"POST /api/v3/repos/o/r/pulls": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(422) }),
		"GET /api/v3/repos/o/r/pulls": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if lists.Add(1) == 1 {
				w.WriteHeader(503)
				return
			}
			json.NewEncoder(w).Encode([]map[string]any{headOf(4, "feat", "o/r")})
		}),
	})
	srv, r := Server{Kind: GitHub, API: "https://" + host + "/api/v3"}, Repo{Host: host, Path: "o/r"}
	_, err := c.OpenPull(ctx, srv, r, PullRequest{Head: "feat", Base: "main", Title: "t"})
	if fe, ok := err.(*Error); !ok || fe.Status != 503 {
		t.Fatalf("the lookup's failure: %#v", err)
	}
	if p, err := c.OpenPull(ctx, srv, r, PullRequest{Head: "feat", Base: "main", Title: "t"}); err != nil || p.Number != 4 || !p.Existing {
		t.Fatalf("the retry: %+v %v", p, err)
	}
}
