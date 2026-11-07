package node

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/forge"
)

// An agent's repository's forge through the node: pulls, checks of its branch, a log masked; not
// for tokens.
func TestNodeForge(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/version":
			json.NewEncoder(w).Encode(map[string]string{"version": "1.24.0"})
		case "/api/v1/repos/o/r/pulls":
			json.NewEncoder(w).Encode([]map[string]any{{"number": 1, "title": "t", "state": "open", "head": map[string]string{"ref": "main"}}})
		case "/api/v1/repos/o/r/commits/main/statuses":
			json.NewEncoder(w).Encode([]map[string]any{{"id": 1, "context": "ci", "status": "success"}})
		case "/api/v1/repos/o/r/actions/jobs/3/logs":
			w.Write([]byte("export API_TOKEN=supersecret123\nok\n"))
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
	q := ForgeQuery{Dir: dir}
	if _, err := n.Forge(ctx, Principal{Kind: "client", Name: "ci"}, q); code(err) != "refused" {
		t.Fatalf("%v", err)
	}
	info, err := n.Forge(ctx, me, q)
	if err != nil || info.Server.Kind != forge.Forgejo || info.Branch != "main" || info.Repo.Path != "o/r" {
		t.Fatalf("%+v %v", info, err)
	}
	if pulls, err := n.Pulls(ctx, me, q, ""); err != nil || len(pulls) != 1 {
		t.Fatalf("%+v %v", pulls, err)
	}
	if checks, err := n.Checks(ctx, me, q, ""); err != nil || len(checks) != 1 || checks[0].Ref != "main" {
		t.Fatalf("checks of the branch: %+v %v", checks, err)
	}
	if text, err := n.CILog(ctx, me, q, "3", ""); err != nil || strings.Contains(text, "supersecret123") || !strings.Contains(text, "ok") {
		t.Fatalf("%q %v", text, err)
	}
	if _, err := n.Forge(ctx, me, ForgeQuery{Dir: t.TempDir()}); code(err) != "not_found" {
		t.Fatalf("no repository: %v", err)
	}
}
