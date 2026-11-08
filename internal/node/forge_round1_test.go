package node

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/forge"
)

// A pull request opened without a base goes into the repository's default branch, asked of the
// forge (master, develop…), not an assumed main.
func TestOpenPullIntoTheDefaultBranch(t *testing.T) {
	var gotBase string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method + " " + r.URL.Path {
		case "GET /api/v1/version":
			json.NewEncoder(w).Encode(map[string]string{"version": "11.0"})
		case "GET /api/v1/repos/o/r":
			json.NewEncoder(w).Encode(map[string]string{"default_branch": "develop"})
		case "POST /api/v1/repos/o/r/pulls":
			var b map[string]any
			json.NewDecoder(r.Body).Decode(&b)
			gotBase, _ = b["base"].(string)
			json.NewEncoder(w).Encode(map[string]any{"number": 3, "html_url": "u3"})
		default:
			w.WriteHeader(404)
		}
	}))
	defer srv.Close()
	host := strings.TrimPrefix(srv.URL, "https://")
	dir := testRepo(t)
	gitIn(t, dir, "remote", "add", "origin", "https://"+host+"/o/r.git")
	n := newNode(t, inboxCfg)
	c, _ := n.forgeClient()
	c.HTTP = srv.Client()
	c.Token = func(string) string { return "" }
	p, err := n.OpenPull(ctx, me, ForgeQuery{Dir: dir}, forge.PullRequest{Head: "feat", Title: "t"})
	if err != nil || p.Number != 3 || gotBase != "develop" || p.Base != "develop" {
		t.Fatalf("%+v %v, base %q", p, err, gotBase)
	}
}
