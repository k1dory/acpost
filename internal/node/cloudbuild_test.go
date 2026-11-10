package node

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

// fakeCatalogue is agent-sandbox's catalogue as the builder uses it: whoami, the lists, plan and
// apply of one AgentDefinition, delete.
type fakeCatalogue struct {
	t  *testing.T
	mu sync.Mutex
	// agents: name → revision and spec; state moves the plan's fingerprint.
	agents      map[string]map[string]any
	applies     int
	applyStatus string // "" = done
	noConns     bool
	deleted     []string
	token       string
}

func (f *fakeCatalogue) fingerprint(doc map[string]any) string {
	b, _ := json.Marshal(doc)
	rev := 0
	if a := f.agents[doc["name"].(string)]; a != nil {
		rev = a["revision"].(int)
	}
	return fmt.Sprintf("plan-%d-%x", rev, len(b))
}

func (f *fakeCatalogue) plan(doc map[string]any) map[string]any {
	name, _ := doc["name"].(string)
	spec, _ := doc["spec"].(map[string]any)
	ch := map[string]any{"kind": "AgentDefinition", "name": name, "action": "create", "current_revision": 0, "next_revision": 1}
	valid := true
	if cur := f.agents[name]; cur != nil {
		rev := cur["revision"].(int)
		ch["action"], ch["current_revision"], ch["next_revision"] = "update", rev, rev+1
		ch["diff"] = []map[string]any{{"path": "spec.instructions", "from": cur["instructions"], "to": spec["instructions"]}, {"path": "spec.connections", "to": spec["connections"]}}
		if cur["instructions"] == spec["instructions"] {
			ch["action"], ch["next_revision"], ch["diff"] = "noop", rev, nil
		}
	}
	if spec["policy"] != "backend-dev" {
		valid = false
		ch["errors"] = []string{fmt.Sprintf("policy %v: not the tenant's", spec["policy"])}
	}
	return map[string]any{"plan": f.fingerprint(doc), "valid": valid, "changes": []any{ch}}
}

func (f *fakeCatalogue) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if r.Header.Get("Authorization") != "Bearer "+f.token {
		w.WriteHeader(401)
		json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		Documents []map[string]any `json:"documents"`
		Plan, Key string
	}
	if r.Body != nil && r.Method == "POST" {
		_ = json.NewDecoder(r.Body).Decode(&body)
	}
	answer := func(code int, v any) { w.WriteHeader(code); json.NewEncoder(w).Encode(v) }
	switch r.Method + " " + r.URL.Path {
	case "GET /v1/whoami":
		answer(200, map[string]any{"tenant": "acme", "admin": false, "subject": "key:key_1"})
	case "GET /v1/agents":
		list := []map[string]any{{"tenant": "", "name": "reviewer", "revision": 2, "spec": map[string]any{"policy": "review", "environment": "shared/base@1"}}}
		for name, a := range f.agents {
			list = append(list, map[string]any{"tenant": "acme", "name": name, "revision": a["revision"], "description": "the shop",
				"spec": map[string]any{"policy": "backend-dev", "environment": "python-dev", "instructions": a["instructions"], "connections": []string{"github"}}})
		}
		answer(200, list)
	case "GET /v1/environments":
		gone := time.Now()
		answer(200, []map[string]any{{"tenant": "acme", "name": "python-dev", "revision": 3, "description": "Python on\nClaude"}, {"tenant": "", "name": "base", "revision": 1},
			{"tenant": "acme", "name": "old", "revision": 1, "deleted_at": gone}})
	case "GET /v1/connections":
		if f.noConns {
			answer(503, map[string]string{"error": "connections disabled"})
			return
		}
		answer(200, []map[string]any{{"tenant": "acme", "name": "github", "revision": 1}})
	case "GET /v1/policies":
		answer(200, []string{"review", "backend-dev"})
	case "POST /v1/plan":
		answer(200, f.plan(body.Documents[0]))
	case "POST /v1/apply":
		doc := body.Documents[0]
		if body.Plan != f.fingerprint(doc) {
			answer(409, map[string]string{"error": "the plan does not match the state"})
			return
		}
		if body.Key != "acpost-"+body.Plan {
			answer(400, map[string]string{"error": "no key"})
			return
		}
		f.applies++
		name := doc["name"].(string)
		pl := f.plan(doc)["changes"].([]any)[0].(map[string]any)
		status := "done"
		if f.applyStatus != "" {
			status = f.applyStatus
		} else {
			f.agents[name] = map[string]any{"revision": pl["next_revision"].(int), "instructions": doc["spec"].(map[string]any)["instructions"]}
		}
		answer(200, map[string]any{"id": "apply_1", "status": status, "error": map[bool]string{true: "", false: "a step failed"}[status == "done"],
			"steps": []map[string]any{{"kind": "AgentDefinition", "tenant": "acme", "name": name, "action": pl["action"], "status": status, "revision": pl["next_revision"]}}})
	default:
		if name, ok := strings.CutPrefix(r.URL.Path, "/v1/agents/"); ok && r.Method == "DELETE" {
			if f.agents[name] == nil {
				answer(404, map[string]string{"error": "not found"})
				return
			}
			delete(f.agents, name)
			f.deleted = append(f.deleted, name)
			w.WriteHeader(204)
			return
		}
		answer(404, map[string]string{"error": "no route " + r.Method + " " + r.URL.Path})
	}
}

func catalogueNode(t *testing.T, f *fakeCatalogue, acp bool) *Node {
	t.Helper()
	f.t, f.token = t, "sbk_test"
	if f.agents == nil {
		f.agents = map[string]map[string]any{}
	}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	env := filepath.Join(t.TempDir(), "cloud.env")
	os.WriteFile(env, []byte("SANDBOXD_URL="+srv.URL+"\nSANDBOXD_TOKEN=sbk_test\n"), 0o600)
	if acp {
		return withCloud(t, acpNode(t, ""), env)
	}
	return newNode(t, "cloud: {env_file: "+env+"}\n")
}

// withCloud is the node again with a cloud (acpNode has no place for one).
func withCloud(t *testing.T, n *Node, env string) *Node {
	t.Helper()
	c := &config.Cloud{EnvFile: env}
	b, _ := os.ReadFile(env)
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, "SANDBOXD_URL="); ok {
			c.URL = v
		}
		if v, ok := strings.CutPrefix(l, "SANDBOXD_TOKEN="); ok {
			c.Token = v
		}
	}
	api, err := newCloudAPI(c)
	if err != nil {
		t.Fatal(err)
	}
	n.cfg.Cloud, n.cloud = c, api
	return n
}

// The builder: who the key is, what a definition is written from, a check that writes nothing,
// a deploy that writes what the check showed and nothing once the catalogue moved on.
func TestCloudBuild(t *testing.T) {
	f := &fakeCatalogue{agents: map[string]map[string]any{"backend": {"revision": 4, "instructions": "old"}}}
	n := catalogueNode(t, f, false)

	info, err := n.CloudInfo(ctx, me)
	if err != nil || !info.Configured || info.Tenant != "acme" || info.Admin || info.Subject != "key:key_1" || info.Error != "" {
		t.Fatalf("%+v %v", info, err)
	}
	for _, p := range []Principal{{Kind: "agent", Name: "x"}, {Kind: "client", Name: "ci"}, {Kind: "node", Name: "hub"}} {
		if _, err := n.CloudInfo(ctx, p); code(err) != "refused" {
			t.Fatalf("%v read the cloud: %v", p, err)
		}
		if _, err := n.CloudDeployAgent(ctx, p, client.CloudAgentSpec{Name: "backend", Policy: "backend-dev"}, "plan"); code(err) != "refused" {
			t.Fatalf("%v deployed: %v", p, err)
		}
		if err := n.CloudDeleteAgent(ctx, p, "backend"); code(err) != "refused" {
			t.Fatalf("%v deleted: %v", p, err)
		}
	}

	cat, err := n.CloudCatalogue(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	if len(cat.Agents) != 2 || cat.Agents[0].Ref != "shared/reviewer" || !cat.Agents[0].Shared || cat.Agents[0].Connections == nil ||
		cat.Agents[1].Ref != "backend" || cat.Agents[1].Revision != 4 || cat.Agents[1].Instructions != "old" || cat.Agents[1].Environment != "python-dev" {
		t.Fatalf("agents %+v", cat.Agents)
	}
	if len(cat.Environments) != 2 || cat.Environments[1].Ref != "shared/base" || len(cat.Connections) != 1 || strings.Join(cat.Policies, ",") != "backend-dev,review" {
		t.Fatalf("%+v", cat) // a deleted environment is not one to choose
	}

	spec := client.CloudAgentSpec{Name: "backend", Description: " the shop ", Policy: "backend-dev", Environment: "python-dev", Instructions: "new\n", Connections: []string{"github", " github ", ""}}
	plan, err := n.CloudPlanAgent(ctx, me, spec)
	if err != nil || !plan.Valid || plan.Action != "update" || plan.Revision != 4 || plan.NextRevision != 5 || len(plan.Diff) != 2 ||
		plan.Diff[0] != (client.CloudDiff{Path: "spec.instructions", From: "old", To: "new"}) || plan.Diff[1].To != `["github"]` {
		t.Fatalf("%+v %v", plan, err)
	}
	if f.applies != 0 {
		t.Fatal("a check wrote")
	}
	// A definition the cloud refuses: its reasons, not an error of the call.
	bad, err := n.CloudPlanAgent(ctx, me, client.CloudAgentSpec{Name: "backend", Policy: "prod-admin", Environment: "python-dev"})
	if err != nil || bad.Valid || len(bad.Errors) != 1 || !strings.Contains(bad.Errors[0], "prod-admin") {
		t.Fatalf("%+v %v", bad, err)
	}
	for _, name := range []string{"", "Backend", "a/b", "../x", "-a", strings.Repeat("a", 64)} {
		if _, err := n.CloudPlanAgent(ctx, me, client.CloudAgentSpec{Name: name, Policy: "backend-dev"}); code(err) != "invalid" {
			t.Fatalf("name %q: %v", name, err)
		}
	}
	// No deploy without the check it follows.
	if _, err := n.CloudDeployAgent(ctx, me, spec, ""); code(err) != "invalid" {
		t.Fatalf("a deploy without a plan: %v", err)
	}
	dep, err := n.CloudDeployAgent(ctx, me, spec, plan.Plan)
	if err != nil || dep != (client.CloudDeployed{Ref: "backend", Revision: 5, Action: "update"}) {
		t.Fatalf("%+v %v", dep, err)
	}
	// The catalogue moved on (this very deploy): the same plan writes nothing more.
	if _, err := n.CloudDeployAgent(ctx, me, client.CloudAgentSpec{Name: "backend", Policy: "backend-dev", Environment: "python-dev", Instructions: "newer"}, plan.Plan); code(err) != "conflict" || hintOf(err) == "" {
		t.Fatalf("a stale plan: %v", err)
	}
	if f.applies != 1 || f.agents["backend"]["instructions"] != "new" {
		t.Fatalf("applies %d, %v", f.applies, f.agents["backend"])
	}
	// An apply the cloud did not finish is not a deploy.
	f.applyStatus = "failed"
	p2, _ := n.CloudPlanAgent(ctx, me, client.CloudAgentSpec{Name: "docs", Policy: "backend-dev", Environment: "python-dev"})
	if p2.Action != "create" || p2.NextRevision != 1 {
		t.Fatalf("%+v", p2)
	}
	if _, err := n.CloudDeployAgent(ctx, me, client.CloudAgentSpec{Name: "docs", Policy: "backend-dev", Environment: "python-dev"}, p2.Plan); code(err) != "unavailable" || !strings.Contains(err.Error(), "failed") {
		t.Fatalf("a failed apply: %v", err)
	}
	f.applyStatus = ""

	if err := n.CloudDeleteAgent(ctx, me, "backend"); err != nil || len(f.deleted) != 1 {
		t.Fatalf("%v %v", err, f.deleted)
	}
	if err := n.CloudDeleteAgent(ctx, me, "backend"); code(err) != "not_found" {
		t.Fatalf("deleted twice: %v", err)
	}
	if err := n.CloudDeleteAgent(ctx, me, "../sandboxes/sb_1"); code(err) != "invalid" {
		t.Fatalf("a path for a name: %v", err)
	}
}

// A cloud without the connections service has none; a key the cloud refuses is said, not hidden.
func TestCloudBuildEdges(t *testing.T) {
	f := &fakeCatalogue{noConns: true}
	n := catalogueNode(t, f, false)
	cat, err := n.CloudCatalogue(ctx, me)
	if err != nil || len(cat.Connections) != 0 || cat.Connections == nil || len(cat.Agents) != 1 {
		t.Fatalf("%+v %v", cat, err)
	}
	f.mu.Lock()
	f.token = "another"
	f.mu.Unlock()
	info, err := n.CloudInfo(ctx, me)
	if err != nil || !info.Configured || info.ErrorCode != "refused" || info.Error == "" || info.Tenant != "" {
		t.Fatalf("%+v %v", info, err)
	}
	if _, err := n.CloudCatalogue(ctx, me); code(err) != "refused" {
		t.Fatalf("%v", err)
	}
	// No cloud at all: not configured, and nothing to build in.
	bare := newNode(t, "")
	if info, err := bare.CloudInfo(ctx, me); err != nil || info.Configured {
		t.Fatalf("%+v %v", info, err)
	}
	if _, err := bare.CloudPlanAgent(ctx, me, client.CloudAgentSpec{Name: "a"}); code(err) != "not_found" {
		t.Fatalf("%v", err)
	}
}

// CheckCloud: a key is asked about before it is kept; what is not an address or a key never leaves.
func TestCheckCloud(t *testing.T) {
	f := &fakeCatalogue{t: t, token: "sbk_good", agents: map[string]map[string]any{}}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	info, err := CheckCloud(ctx, &config.Cloud{URL: srv.URL + "/", Token: "sbk_good"})
	if err != nil || info.Tenant != "acme" || info.URL != srv.URL {
		t.Fatalf("%+v %v", info, err)
	}
	if _, err := CheckCloud(ctx, &config.Cloud{URL: srv.URL, Token: "sbk_bad"}); code(err) != "refused" {
		t.Fatalf("a wrong key: %v", err)
	}
	for _, c := range []config.Cloud{{URL: "ftp://x", Token: "k"}, {URL: "https://u:p@host", Token: "k"}, {URL: "host", Token: "k"}, {URL: srv.URL, Token: ""}, {URL: srv.URL, Token: "a b"}, {URL: srv.URL, Token: "a\nX-Evil: 1"}} {
		if _, err := CheckCloud(ctx, &c); code(err) != "invalid" {
			t.Fatalf("%+v: %v", c.URL, err)
		}
	}
}

// A draft: an agent of the node writes the definition, read-only; its answer is the form's, and
// nothing is deployed by it.
func TestCloudDraft(t *testing.T) {
	f := &fakeCatalogue{}
	n := catalogueNode(t, f, true)
	if _, err := n.CloudDraft(ctx, me, client.CloudDraftRequest{Agent: "agent:fake", Ask: "a backend developer"}); code(err) != "unsupported" {
		t.Fatalf("no read-only mode: %v", err)
	}
	t.Setenv("FAKE_MODES", "1")
	n = catalogueNode(t, f, true)
	for _, r := range []client.CloudDraftRequest{{Agent: "agent:fake", Ask: " "}, {Agent: "agent:fake", Ask: strings.Repeat("x", cloudDraftAsk+1)},
		{Agent: "agent:nobody", Ask: "x"}, {Agent: "agent:fake@other", Ask: "x"}, {Agent: "agent:fake", Ask: "x", Cwd: "/nowhere/at/all"}} {
		if _, err := n.CloudDraft(ctx, me, r); code(err) != "invalid" {
			t.Fatalf("%+v: %v", r.Agent, err)
		}
	}
	if _, err := n.CloudDraft(ctx, Principal{Kind: "agent", Name: "fake"}, client.CloudDraftRequest{Agent: "agent:fake", Ask: "x"}); code(err) != "refused" {
		t.Fatalf("an agent drafted: %v", err)
	}
	d, err := n.CloudDraft(ctx, me, client.CloudDraftRequest{Agent: "agent:fake", Ask: "a backend developer", Cwd: t.TempDir(),
		Current: &client.CloudAgentSpec{Name: "backend", Policy: "backend-dev"}})
	if err != nil || d.Msg == "" || d.Thread == "" {
		t.Fatalf("%+v %v", d, err)
	}
	m, err := n.Get(ctx, me, d.Msg)
	if err != nil || m.Meta["acp_mode"] != "plan" || m.Meta["acp_mode_strict"] != "1" || m.Meta[metaCloudDraft] == "" {
		t.Fatalf("%+v %v", m.Meta, err)
	}
	for _, want := range []string{"a backend developer", "backend-dev", "python-dev: Python on Claude", "shared/base", "github", `"name": "backend"`, "working directory"} {
		if !strings.Contains(m.Body, want) {
			t.Fatalf("the prompt has no %q:\n%s", want, m.Body)
		}
	}
	if w, err := n.Wait(ctx, me, d.Msg, 30*time.Second); err != nil || w.State != store.Done {
		t.Fatalf("%+v %v", w, err)
	}
	res, err := n.CloudDraftResult(ctx, me, d.Msg)
	if err != nil || res.State != "done" || res.Error != "" || res.Spec == nil {
		t.Fatalf("%+v %v", res, err)
	}
	if s := *res.Spec; s.Name != "drafted" || s.Policy != "backend-dev" || s.Environment != "shared/base" || s.Instructions != "You work on the shop.\nRun make test." || len(s.Connections) != 1 {
		t.Fatalf("%+v", s)
	}
	if f.applies != 0 {
		t.Fatal("a draft deployed")
	}
	// A message that is no draft is not read as one.
	other, _, err := n.Send(ctx, me, SendRequest{To: "agent:fake", Body: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.CloudDraftResult(ctx, me, other.ID); code(err) != "not_found" {
		t.Fatalf("%v", err)
	}
}

func TestParseCloudDraft(t *testing.T) {
	s, err := parseCloudDraft("Note.\n```json\n{\"name\": \"\"}\n```\nthen\n```json\n{\"name\": \" docs \", \"instructions\": \"a ``` b\", \"extra\": 1}\n```\n```json\n{broken\n```")
	if err != nil || s.Name != "docs" || s.Instructions != "a ``` b" || s.Connections == nil {
		t.Fatalf("%+v %v", s, err)
	}
	if s, err := parseCloudDraft("```\n{\"policy\": \"p\"}\n```"); err != nil || s.Policy != "p" {
		t.Fatalf("%+v %v", s, err)
	}
	for _, reply := range []string{"", "I could not decide.", "```json\n{\"name\": \"\"}\n```", "```json\n[1]\n```"} {
		if _, err := parseCloudDraft(reply); err == nil {
			t.Fatalf("%q read as a definition", reply)
		}
	}
	if _, err := parseCloudDraft("```json\n{\"name\": \"a\", \"instructions\": \"" + strings.Repeat("x", cloudInstrMax+1) + "\"}\n```"); err == nil || !strings.Contains(err.Error(), "over") {
		t.Fatalf("%v", err)
	}
}
