package node

import (
	"cmp"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os/exec"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

// Building the cloud's agents from here. An agent of the cloud is a definition in the tenant's
// catalogue (agent-sandbox's AgentDefinition): the policy it runs under, the environment it runs
// in, the repository, its instructions, its connections. Deploying one is writing a revision of
// that definition: the cloud checks it first (a plan: what would change, nothing written), then
// writes exactly what the check showed. Nothing is built and nothing running changes: a new cloud
// task takes the latest revision, a sandbox already made keeps its own.
//
// An agent of this node can write the definition from a person's words (a draft): it answers in
// its read-only mode, and the answer is only ever put before the person; the deploy is theirs.

// cloudAgentName is a name the cloud takes for a catalogue item (it is also a path segment here).
var cloudAgentName = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

const (
	cloudDraftAsk  = 8 << 10  // a person's words for a draft
	cloudInstrMax  = 64 << 10 // a definition's instructions
	metaCloudDraft = "cloud_draft"
)

// CheckCloud asks a cloud who a key is, before it is written anywhere: the tenant, or the admin.
func CheckCloud(ctx context.Context, c *config.Cloud) (client.CloudInfo, error) {
	u, err := url.Parse(c.URL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil {
		return client.CloudInfo{}, errf("invalid", "the cloud's address: https://host[:port]")
	}
	if strings.TrimSpace(c.Token) == "" || strings.ContainsAny(c.Token, " \t\r\n\"'") {
		return client.CloudInfo{}, errf("invalid", "the key: one word, as the cloud printed it (sbk_…)")
	}
	api, err := newCloudAPI(&config.Cloud{URL: strings.TrimRight(c.URL, "/"), Token: c.Token, CA: c.CA})
	if err != nil {
		return client.CloudInfo{}, errf("invalid", "%v", err)
	}
	return api.whoami(ctx)
}

// id names the connection: the address and the key, as a hash that says neither.
func (c *cloudAPI) id() string {
	sum := sha256.Sum256([]byte("acpost cloud\x00" + c.url + "\x00" + c.token))
	return hex.EncodeToString(sum[:8])
}

// cloudFor is the node's cloud when it is the one the caller read from (id): what a person saw
// in one cloud's catalogue is never written to another the node was connected to meanwhile.
func (n *Node) cloudFor(id string) (*cloudAPI, error) {
	if n.cloud == nil {
		return nil, errh("not_found", "connect a cloud in Settings", "no cloud is configured")
	}
	if id != n.cloud.id() {
		return nil, errh("conflict", "open the cloud's settings again: they show the cloud connected now",
			"the node is connected to another cloud (or by another key) than the one this was read from")
	}
	return n.cloud, nil
}

func (c *cloudAPI) whoami(ctx context.Context) (client.CloudInfo, error) {
	out := client.CloudInfo{Configured: true, URL: c.url, ID: c.id()}
	var who struct {
		Tenant  string     `json:"tenant"`
		Admin   bool       `json:"admin"`
		Subject string     `json:"subject"`
		Expires *time.Time `json:"expires"`
	}
	if err := c.call(ctx, "GET", "/v1/whoami", nil, &who); err != nil {
		return out, err
	}
	out.Tenant, out.Admin, out.Subject, out.Expires = who.Tenant, who.Admin, who.Subject, who.Expires
	return out, nil
}

// CloudInfo says which cloud the node is connected to and as whom. A cloud that refuses the key
// or does not answer is still the configured one: its error is in the answer.
func (n *Node) CloudInfo(ctx context.Context, p Principal) (client.CloudInfo, error) {
	if err := userOnly(p, "the cloud"); err != nil {
		return client.CloudInfo{}, err
	}
	if n.cloud == nil {
		return client.CloudInfo{}, nil
	}
	out, err := n.cloud.whoami(ctx)
	if err != nil {
		out.Error, out.ErrorCode = err.Error(), CodeOf(err)
	}
	return out, nil
}

// cloudCatalogItem is a catalogue item as the cloud lists it.
type cloudCatalogItem struct {
	Tenant      string          `json:"tenant"`
	Name        string          `json:"name"`
	Revision    int             `json:"revision"`
	Description string          `json:"description"`
	Spec        json.RawMessage `json:"spec"`
	DeletedAt   *time.Time      `json:"deleted_at"`
}

func (it cloudCatalogItem) item() client.CloudItem {
	out := client.CloudItem{Ref: it.Name, Name: it.Name, Shared: it.Tenant == "", Revision: it.Revision, Description: it.Description}
	if out.Shared {
		out.Ref = "shared/" + it.Name
	}
	return out
}

// cloudSpec is an AgentDefinition's spec on the cloud's wire.
type cloudSpec struct {
	Policy       string   `json:"policy"`
	Environment  string   `json:"environment"`
	Repo         string   `json:"repo,omitempty"`
	Instructions string   `json:"instructions,omitempty"`
	Connections  []string `json:"connections,omitempty"`
}

// CloudCatalogue reads what a definition is written from. The admin's key reads the shared
// catalogue (its own scope); a tenant's reads its own items and the shared ones.
func (n *Node) CloudCatalogue(ctx context.Context, p Principal) (client.CloudCatalogue, error) {
	out := client.CloudCatalogue{Agents: []client.CloudAgentDef{}, Environments: []client.CloudItem{}, Connections: []client.CloudItem{}, Policies: []string{}}
	if err := userOnly(p, "the cloud"); err != nil {
		return out, err
	}
	if n.cloud == nil {
		return out, errh("not_found", "connect a cloud in Settings", "no cloud is configured")
	}
	out.Cloud = n.cloud.id()
	var agents, envs, conns []cloudCatalogItem
	if err := n.cloud.call(ctx, "GET", "/v1/agents", nil, &agents); err != nil {
		return out, err
	}
	if err := n.cloud.call(ctx, "GET", "/v1/environments", nil, &envs); err != nil {
		return out, err
	}
	// A cloud without connections (503: that service is off) has none; one that fails is an error.
	if code, err := n.cloud.callStatus(ctx, "GET", "/v1/connections", nil, &conns); err != nil && code != 503 {
		return out, err
	}
	if err := n.cloud.call(ctx, "GET", "/v1/policies", nil, &out.Policies); err != nil {
		return out, err
	}
	if out.Policies == nil {
		out.Policies = []string{}
	}
	slices.Sort(out.Policies)
	for _, it := range agents {
		if it.DeletedAt != nil {
			continue
		}
		var s cloudSpec
		if err := json.Unmarshal(it.Spec, &s); err != nil {
			return out, errf("unavailable", "the cloud: agent %s: not a definition (%v)", it.Name, err)
		}
		i := it.item()
		out.Agents = append(out.Agents, client.CloudAgentDef{Ref: i.Ref, Shared: i.Shared, Revision: it.Revision,
			CloudAgentSpec: client.CloudAgentSpec{Name: it.Name, Description: it.Description, Policy: s.Policy, Environment: s.Environment,
				Repo: s.Repo, Instructions: s.Instructions, Connections: nonNil(s.Connections)}})
	}
	for _, it := range envs {
		if it.DeletedAt == nil {
			out.Environments = append(out.Environments, it.item())
		}
	}
	for _, it := range conns {
		if it.DeletedAt == nil {
			out.Connections = append(out.Connections, it.item())
		}
	}
	return out, nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// cloudDocument is a definition as the cloud takes it: checked here only as far as the cloud
// cannot say it better (a name that is a path, sizes); the rest is the plan's to refuse.
func cloudDocument(a client.CloudAgentSpec) (map[string]any, error) {
	a.Name = strings.TrimSpace(a.Name)
	if !cloudAgentName.MatchString(a.Name) {
		return nil, errf("invalid", "the agent's name: lowercase letters, digits and dashes, up to 63")
	}
	if len(a.Instructions) > cloudInstrMax {
		return nil, errf("invalid", "the instructions are %d bytes, over %d", len(a.Instructions), cloudInstrMax)
	}
	spec := cloudSpec{Policy: strings.TrimSpace(a.Policy), Environment: strings.TrimSpace(a.Environment), Repo: strings.TrimSpace(a.Repo),
		Instructions: strings.TrimSpace(a.Instructions)}
	for _, c := range a.Connections {
		if c = strings.TrimSpace(c); c != "" && !slices.Contains(spec.Connections, c) {
			spec.Connections = append(spec.Connections, c)
		}
	}
	doc := map[string]any{"kind": "AgentDefinition", "name": a.Name, "spec": spec}
	if d := strings.TrimSpace(a.Description); d != "" {
		doc["description"] = d
	}
	return doc, nil
}

// cloudPlan is the cloud's plan of a bundle, as far as one document's goes.
type cloudPlan struct {
	Plan    string   `json:"plan"`
	Valid   bool     `json:"valid"`
	Errors  []string `json:"errors"`
	Changes []struct {
		Kind            string   `json:"kind"`
		Name            string   `json:"name"`
		Action          string   `json:"action"`
		CurrentRevision int      `json:"current_revision"`
		NextRevision    int      `json:"next_revision"`
		Errors          []string `json:"errors"`
		Diff            []struct {
			Path string `json:"path"`
			From any    `json:"from"`
			To   any    `json:"to"`
		} `json:"diff"`
	} `json:"changes"`
}

// CloudPlanAgent has the cloud check a definition against the tenant's rights and the catalogue:
// what a deploy would change, and what it would refuse. Nothing is written.
func (n *Node) CloudPlanAgent(ctx context.Context, p Principal, cloud string, a client.CloudAgentSpec) (client.CloudPlan, error) {
	out := client.CloudPlan{Diff: []client.CloudDiff{}, Errors: []string{}}
	if err := userOnly(p, "the cloud"); err != nil {
		return out, err
	}
	api, err := n.cloudFor(cloud)
	if err != nil {
		return out, err
	}
	doc, err := cloudDocument(a)
	if err != nil {
		return out, err
	}
	var pl cloudPlan
	if err := api.call(ctx, "POST", "/v1/plan", map[string]any{"documents": []any{doc}}, &pl); err != nil {
		return out, err
	}
	out.Plan, out.Valid = pl.Plan, pl.Valid
	out.Errors = append(out.Errors, pl.Errors...)
	for _, c := range pl.Changes {
		if c.Kind != "AgentDefinition" || c.Name != doc["name"] {
			continue
		}
		out.Action, out.Revision, out.NextRevision = c.Action, c.CurrentRevision, c.NextRevision
		out.Errors = append(out.Errors, c.Errors...)
		for _, d := range c.Diff {
			out.Diff = append(out.Diff, client.CloudDiff{Path: d.Path, From: planValue(d.From), To: planValue(d.To)})
		}
	}
	if out.Valid && out.Action == "" {
		// A plan that says nothing of the document is not one to deploy by.
		out.Valid = false
		out.Errors = append(out.Errors, "the cloud's plan does not name the agent")
	}
	return out, nil
}

// planValue is a field's value in a plan's diff, as text.
func planValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// CloudDeployAgent writes a definition into the catalogue as the plan (CloudPlanAgent's) showed
// it: a catalogue that has moved on since is a conflict, and nothing is written. Deployed again
// with the same plan (the answer lost), it is the same apply, continued: one revision.
func (n *Node) CloudDeployAgent(ctx context.Context, p Principal, cloud string, a client.CloudAgentSpec, plan string) (client.CloudDeployed, error) {
	if err := userOnly(p, "the cloud"); err != nil {
		return client.CloudDeployed{}, err
	}
	api, err := n.cloudFor(cloud)
	if err != nil {
		return client.CloudDeployed{}, err
	}
	if plan == "" || len(plan) > maxLabel {
		return client.CloudDeployed{}, errh("invalid", "check the definition first", "a deploy names the check it follows")
	}
	doc, err := cloudDocument(a)
	if err != nil {
		return client.CloudDeployed{}, err
	}
	var ap struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		Error  string `json:"error"`
		Steps  []struct {
			Kind     string `json:"kind"`
			Tenant   string `json:"tenant"`
			Name     string `json:"name"`
			Action   string `json:"action"`
			Status   string `json:"status"`
			Revision int    `json:"revision"`
			Error    string `json:"error"`
		} `json:"steps"`
	}
	// The key is the plan: the cloud keeps one apply per key, and a plan names one bundle on one state.
	code, err := api.callStatus(ctx, "POST", "/v1/apply", map[string]any{"documents": []any{doc}, "plan": plan, "key": "acpost-" + plan}, &ap)
	if code == 409 || code == 422 {
		return client.CloudDeployed{}, errh("conflict", "check it again: the plan shows what a deploy would do now",
			"the catalogue changed since the check: %v", err)
	}
	if err != nil {
		return client.CloudDeployed{}, err
	}
	for _, s := range ap.Steps {
		if s.Kind != "AgentDefinition" || s.Name != doc["name"] {
			continue
		}
		if ap.Status != "done" || (s.Status != "done" && s.Status != "skipped") {
			break
		}
		ref := s.Name
		if s.Tenant == "" {
			ref = "shared/" + s.Name
		}
		return client.CloudDeployed{Ref: ref, Revision: s.Revision, Action: s.Action}, nil
	}
	return client.CloudDeployed{}, errh("unavailable", "look at it in the catalogue, then check and deploy again",
		"the cloud's apply %s is %s: %s", ap.ID, cmp.Or(ap.Status, "unknown"), cmp.Or(ap.Error, "it did not say why"))
}

// CloudDeleteAgent takes an agent out of the catalogue: no new task starts from it; its sandboxes
// and its revisions' history stay.
func (n *Node) CloudDeleteAgent(ctx context.Context, p Principal, cloud, name string) error {
	if err := userOnly(p, "the cloud"); err != nil {
		return err
	}
	api, err := n.cloudFor(cloud)
	if err != nil {
		return err
	}
	if !cloudAgentName.MatchString(name) {
		return errf("invalid", "the agent's name")
	}
	return api.call(ctx, "DELETE", "/v1/agents/"+url.PathEscape(name), nil, nil)
}

// CloudDraft has an ACP agent of this node write a definition from a person's words: an ordinary
// task in a thread of its own (the app can open it), in the agent's read-only mode. Its answer is
// read by CloudDraftResult; nothing reaches the cloud until the person deploys.
func (n *Node) CloudDraft(ctx context.Context, p Principal, r client.CloudDraftRequest) (client.CloudDraft, error) {
	if err := userOnly(p, "the cloud"); err != nil {
		return client.CloudDraft{}, err
	}
	idem := ""
	if r.Key != "" {
		if len(r.Key) > maxLabel-len("cloud-draft:") {
			return client.CloudDraft{}, errf("invalid", "the draft's key is too long")
		}
		idem = "cloud-draft:" + r.Key
		// Asked before under this key: that task, whatever the cloud or the agent say now (its
		// answer was lost; the person stops it, or waits for it, by this).
		if prev, err := n.st.ByIdem(ctx, p.Address(), idem); err == nil && prev.Meta[metaCloudDraft] != "" {
			return client.CloudDraft{Msg: prev.ID, Thread: prev.Thread, State: string(prev.State)}, nil
		}
	}
	if _, err := n.cloudFor(r.Cloud); err != nil {
		return client.CloudDraft{}, err
	}
	ask := strings.TrimSpace(r.Ask)
	if ask == "" || len(ask) > cloudDraftAsk {
		return client.CloudDraft{}, errf("invalid", "what the agent should do: up to %d bytes", cloudDraftAsk)
	}
	to, err := ParseAddress(r.Agent)
	if err != nil || to.Kind != "agent" || to.Node != "" || n.acp[to.Name] == nil {
		return client.CloudDraft{}, errf("invalid", "%q: an ACP agent of this node (agent:claude, agent:codex)", r.Agent)
	}
	dir, remote := "", ""
	if r.Cwd != "" {
		if dir, err = checkDir(r.Cwd); err != nil {
			return client.CloudDraft{}, err
		}
		remote = originOf(ctx, dir)
	}
	mode := n.readOnlyMode(ctx, p, to.Name)
	if mode == "" {
		return client.CloudDraft{}, errh("unsupported", "choose an agent with a plan or read-only mode (Claude Code, Codex)",
			"%s has no read-only mode: it would ask to run commands while it reads", to)
	}
	cat, err := n.CloudCatalogue(ctx, p)
	if err != nil {
		return client.CloudDraft{}, err
	}
	m, _, err := n.Send(ctx, p, SendRequest{To: to.String(), Body: cloudDraftPrompt(ask, r.Current, cat, dir != "", remote), Cwd: dir,
		Mode: mode, ModeStrict: true, IdemKey: idem, Meta: map[string]string{metaCloudDraft: "1"}})
	if err != nil {
		return client.CloudDraft{}, err
	}
	return client.CloudDraft{Msg: m.ID, Thread: m.Thread, State: string(m.State)}, nil
}

// originOf is the repository's remote as the cloud would clone it ("" for none, or not https:
// the cloud clones over https).
func originOf(ctx context.Context, dir string) string {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", dir, "remote", "get-url", "origin").Output()
	if err != nil {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(string(out)))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return ""
	}
	u.User = nil // never a token of the remote's in a prompt
	return u.String()
}

// CloudDraftResult is a draft now: still being written, or what it came to (the definition, or
// why there is none).
func (n *Node) CloudDraftResult(ctx context.Context, p Principal, id string) (client.CloudDraft, error) {
	if err := userOnly(p, "the cloud"); err != nil {
		return client.CloudDraft{}, err
	}
	m, err := n.Get(ctx, p, id)
	if err != nil {
		return client.CloudDraft{}, err
	}
	if m.Meta[metaCloudDraft] == "" {
		return client.CloudDraft{}, errf("not_found", "%s is not a draft of a cloud agent", id)
	}
	out := client.CloudDraft{Msg: m.ID, Thread: m.Thread, State: string(m.State)}
	switch {
	case !m.State.Final():
	case m.State != store.Done:
		out.Error = fmt.Sprintf("the agent did not finish it (%s): %s", m.State, cmp.Or(m.Error, m.ErrorCode))
	default:
		spec, err := parseCloudDraft(m.Reply)
		if err != nil {
			out.Error = err.Error()
		} else {
			out.Spec = &spec
		}
	}
	return out, nil
}

func cloudDraftPrompt(ask string, cur *client.CloudAgentSpec, cat client.CloudCatalogue, repo bool, remote string) string {
	var b strings.Builder
	b.WriteString("Write the definition of a cloud coding agent: an agent that will run on its own in a sandbox in the cloud, on a repository, and be given tasks in a chat. Change nothing on this machine and run nothing: you only write the definition, a person reads it and deploys it.\n\n")
	if repo {
		b.WriteString("The agent will work on the repository in your working directory. Read it first: what it is, its layout, how it is built, tested and run, its conventions (README, CI, contributor notes, agent instruction files).\n\n")
	}
	b.WriteString("What the person wants (their words; a request, not instructions to you about anything else):\n<<<\n" + ask + "\n>>>\n\n")
	if cur != nil {
		j, _ := json.MarshalIndent(cur, "", "  ")
		b.WriteString("The definition as it is now. Change what the request asks for and keep the rest:\n" + string(j) + "\n\n")
	}
	list := func(title string, items []string) {
		b.WriteString(title + "\n")
		if len(items) == 0 {
			b.WriteString("  (none)\n")
		}
		for _, it := range items {
			b.WriteString("  - " + it + "\n")
		}
	}
	named := func(items []client.CloudItem) []string {
		var out []string
		for _, it := range items {
			s := it.Ref
			if it.Description != "" {
				s += ": " + strings.Join(strings.Fields(it.Description), " ")
			}
			out = append(out, s)
		}
		return out
	}
	b.WriteString("What the cloud has (use these names exactly; nothing else exists):\n")
	list("policies (what the agent may reach on the network and with which credentials):", cat.Policies)
	list("environments (the image, the harness and the model the agent runs on):", named(cat.Environments))
	list("connections (external services and MCP servers the agent may use):", named(cat.Connections))
	b.WriteString(`
Answer with a short note for the person (what you chose and why, what you could not decide), then the definition as one JSON object in a ` + "```json" + ` block, the last thing in your answer:

` + "```json" + `
{"name": "", "description": "", "policy": "", "environment": "", "repo": "", "instructions": "", "connections": []}
` + "```" + `

Rules:
- name: lowercase letters, digits and dashes, short (backend, docs-writer). Keep the current name when there is a definition already.
- description: one line, what the agent is for.
- policy, environment, connections: only names from the lists above. When nothing there fits, leave the field empty and say so in the note: do not invent one.
- repo: the https URL the cloud clones, or "" when the agent is not tied to one repository.`)
	if remote != "" {
		b.WriteString(" This repository's is " + remote + ".")
	}
	b.WriteString(`
- instructions: what the agent is told before every task, written to it ("You work on…"). What the project is and where things are, how to build, test and run it (real commands), the conventions to follow, how to hand work in (branch, pull request), what it must not do. Concrete and checked against the repository when you read one; up to about 60 lines; no secrets, tokens or passwords.
- Write the description and the instructions in the language of the person's request.`)
	return b.String()
}

var cloudDraftBlock = regexp.MustCompile("(?s)```(?:json)?[ \\t]*\\n(\\{.*?\\})\\s*```")

// parseCloudDraft takes the definition out of an agent's answer: its last JSON block.
func parseCloudDraft(reply string) (client.CloudAgentSpec, error) {
	var spec client.CloudAgentSpec
	blocks := cloudDraftBlock.FindAllStringSubmatch(reply, -1)
	for i := len(blocks) - 1; i >= 0; i-- {
		var s client.CloudAgentSpec
		if json.Unmarshal([]byte(blocks[i][1]), &s) != nil {
			continue
		}
		s.Name, s.Policy, s.Environment, s.Repo = strings.TrimSpace(s.Name), strings.TrimSpace(s.Policy), strings.TrimSpace(s.Environment), strings.TrimSpace(s.Repo)
		s.Description, s.Instructions = strings.TrimSpace(s.Description), strings.TrimSpace(s.Instructions)
		s.Connections = nonNil(s.Connections)
		if s.Name == "" && s.Instructions == "" && s.Policy == "" && s.Environment == "" {
			continue
		}
		if len(s.Instructions) > cloudInstrMax {
			return spec, fmt.Errorf("the agent's instructions are %d bytes, over %d", len(s.Instructions), cloudInstrMax)
		}
		return s, nil
	}
	return spec, fmt.Errorf("the agent's answer had no definition in the format asked for: open its chat to read it")
}
