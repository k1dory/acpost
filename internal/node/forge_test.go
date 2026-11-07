package node

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/forge"
	"github.com/k1dory/acpx"
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

// An agent opens or merges a pull request once a person approves it; refused, nothing reaches the
// forge. The node's user acts on its own.
func TestForgeAgentNeedsApproval(t *testing.T) {
	var mu sync.Mutex
	var hits []string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v1/version":
			json.NewEncoder(w).Encode(map[string]string{"version": "11.0.0"})
		case r.Method == "POST":
			mu.Lock()
			hits = append(hits, r.URL.Path)
			mu.Unlock()
			w.WriteHeader(201)
			json.NewEncoder(w).Encode(map[string]any{"number": 7, "html_url": "https://x/pulls/7"})
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
	c.Token = func(string) string { return "t" }
	q := ForgeQuery{Dir: dir}

	m, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "ship it"})
	if err != nil {
		t.Fatal(err)
	}
	tok := n.issueAgentToken("bot")
	bot := Principal{Kind: "agent", Name: "bot", agent: tok}
	if _, err := n.OpenPull(ctx, bot, q, forge.PullRequest{Head: "f", Base: "main", Title: "x"}); code(err) != "refused" {
		t.Fatalf("no turn running: %v", err)
	}
	n.setCurrent(tok, m.ID, "")

	answer := func(option string) {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			reqs, _ := n.Asking(me)
			if len(reqs) == 1 {
				if !strings.Contains(reqs[0].Title, "pull request") {
					t.Errorf("asked %q", reqs[0].Title)
				}
				if err := n.Answer(ctx, me, m.ID, acpx.Reply{RequestID: reqs[0].ID, OptionID: option}); err != nil {
					t.Error(err)
				}
				return
			}
		}
		t.Error("nothing asked")
	}
	go answer(approveReject)
	if _, err := n.OpenPull(ctx, bot, q, forge.PullRequest{Head: "f", Base: "main", Title: "x"}); code(err) != "refused" {
		t.Fatalf("rejected: %v", err)
	}
	go answer(approveAllow)
	if p, err := n.OpenPull(ctx, bot, q, forge.PullRequest{Head: "f", Base: "main", Title: "x"}); err != nil || p.Number != 7 {
		t.Fatalf("allowed: %+v %v", p, err)
	}
	go answer(approveAllow)
	if err := n.MergePull(ctx, bot, q, forge.Merge{Number: 7, Method: "squash"}); err != nil {
		t.Fatalf("merge allowed: %v", err)
	}
	// The node's user: no approval.
	if err := n.MergePull(ctx, me, q, forge.Merge{Number: 8}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(hits, " ") != "/api/v1/repos/o/r/pulls /api/v1/repos/o/r/pulls/7/merge /api/v1/repos/o/r/pulls/8/merge" {
		t.Fatalf("forge saw %v", hits)
	}
	evs, _ := n.Events(ctx, me, m.ID, 0, 100)
	var decisions []string
	for _, e := range evs {
		if e.Kind == "permission" {
			var d struct{ Decision string }
			json.Unmarshal([]byte(e.Data), &d)
			decisions = append(decisions, d.Decision)
		}
	}
	if strings.Join(decisions, " ") != "rejected allowed allowed" {
		t.Fatalf("decisions %v", decisions)
	}
}

// An agent that sent the work cannot approve the pull request the agent doing it opens: only the
// node's user can.
func TestForgeApprovalIsAPersons(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/version" {
			json.NewEncoder(w).Encode(map[string]string{"version": "11.0.0"})
			return
		}
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(map[string]any{"number": 1})
	}))
	defer srv.Close()
	dir := testRepo(t)
	gitIn(t, dir, "remote", "add", "origin", "https://"+strings.TrimPrefix(srv.URL, "https://")+"/o/r.git")
	n := newNode(t, inboxCfg)
	c, _ := n.forgeClient()
	c.HTTP, c.Token = srv.Client(), func(string) string { return "t" }

	boss := Principal{Kind: "agent", Name: "picky"}
	m, _, err := n.Send(ctx, boss, SendRequest{To: "bot", Body: "open a PR"})
	if err != nil {
		t.Fatal(err)
	}
	tok := n.issueAgentToken("bot")
	n.setCurrent(tok, m.ID, "")
	done := make(chan error, 1)
	go func() {
		_, err := n.OpenPull(ctx, Principal{Kind: "agent", Name: "bot", agent: tok}, ForgeQuery{Dir: dir}, forge.PullRequest{Head: "f", Base: "main", Title: "x"})
		done <- err
	}()
	var id string
	for deadline := time.Now().Add(5 * time.Second); id == "" && time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if reqs, _ := n.Asking(me); len(reqs) == 1 {
			id = reqs[0].ID
		}
	}
	if id == "" {
		t.Fatal("nothing asked")
	}
	if err := n.Answer(ctx, boss, m.ID, acpx.Reply{RequestID: id, OptionID: approveAllow}); code(err) != "refused" {
		t.Fatalf("the sending agent approved it: %v", err)
	}
	if err := n.Answer(ctx, me, m.ID, acpx.Reply{RequestID: id, OptionID: approveAllow}); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
