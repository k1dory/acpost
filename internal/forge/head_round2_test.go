package forge

import (
	"net/http"
	"testing"
)

// One owner, two repositories with the same branch: owner/repo:branch names one of them, and a
// pull request from the other (listed first) is not the one already open.
func TestAlreadyOpenComparesTheSourceRepository(t *testing.T) {
	for _, kind := range []string{GitHub, Forgejo} {
		api := map[string]string{GitHub: "/api/v3", Forgejo: "/api/v1"}[kind]
		c, host, _ := fake(t, kind, map[string]any{
			"POST " + api + "/repos/o/r/pulls": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(422) }),
			"GET " + api + "/repos/o/r/pulls":  []map[string]any{headOf(1, "feat", "alice/repo-a"), headOf(2, "feat", "alice/repo-b")},
		})
		srv, r := Server{Kind: kind, API: "https://" + host + api}, Repo{Host: host, Path: "o/r"}
		if p, err := c.OpenPull(ctx, srv, r, PullRequest{Head: "alice/repo-b:feat", Base: "main", Title: "t"}); err != nil || p.Number != 2 || !p.Existing {
			t.Fatalf("%s: repo-b's feat: %+v %v", kind, p, err)
		}
		if _, err := c.OpenPull(ctx, srv, r, PullRequest{Head: "alice/repo-c:feat", Base: "main", Title: "t"}); err == nil {
			t.Fatalf("%s: repo-c's feat found in another repository's pull request", kind)
		}
		if p, err := c.OpenPull(ctx, srv, r, PullRequest{Head: "alice:feat", Base: "main", Title: "t"}); err != nil || p.Number != 1 {
			t.Fatalf("%s: alice's fork, any name: %+v %v", kind, p, err)
		}
	}
}
