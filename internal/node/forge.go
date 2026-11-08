package node

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/k1dory/acpost/internal/forge"
	"github.com/k1dory/acpx"
)

// The forge of an agent's repository: its pull requests, CI runs and their logs, a new pull
// request. The repository's remote (origin) names the forge; what kind it is and its version the
// host says (kept a while); tokens are the node's user's (acpost forge login: the keychain on
// macOS). For the node's user and its agents.

// ForgeQuery names a repository: an agent's working directory, or a directory.
type ForgeQuery struct {
	Agent string `json:"agent,omitempty"`
	Dir   string `json:"dir,omitempty"`
}

// ForgeInfo is a repository's forge as the node sees it.
type ForgeInfo struct {
	Repo   forge.Repo   `json:"repo"`
	Server forge.Server `json:"server"`
	Branch string       `json:"branch,omitempty"`
	Authed bool         `json:"authed"` // a token is kept for its host
}

type forges struct {
	once   sync.Once
	client *forge.Client
	store  *forge.Store
	mu     sync.Mutex
	probed map[string]probed
}

type probed struct {
	srv forge.Server
	at  time.Time
}

func (n *Node) forgeClient() (*forge.Client, *forge.Store) {
	n.forges.once.Do(func() {
		st := forge.DefaultStore()
		n.forges.store = st
		n.forges.client = &forge.Client{Token: st.Token, Kind: st.Kind}
		n.forges.probed = map[string]probed{}
	})
	return n.forges.client, n.forges.store
}

// forgeOf finds the repository a query names, its forge and its branch.
func (n *Node) forgeOf(ctx context.Context, p Principal, q ForgeQuery) (ForgeInfo, error) {
	if !p.local() {
		return ForgeInfo{}, errf("refused", "forges are for the node's user")
	}
	dir := q.Dir
	if q.Agent != "" {
		a := n.acp[strings.TrimPrefix(q.Agent, "agent:")]
		if a == nil {
			return ForgeInfo{}, errh("not_found", "`acpost agents` lists them", "no ACP agent %s here", q.Agent)
		}
		dir = (&acpSlot{a: a}).cwd()
	}
	if dir == "" {
		return ForgeInfo{}, errf("invalid", "an agent or a directory")
	}
	out, _, err := git(ctx, dir, nil, 4096, "remote", "get-url", "origin")
	if err != nil {
		return ForgeInfo{}, errh("not_found", "the repository needs a remote named origin", "%s: no origin remote (%v)", dir, err)
	}
	repo, err := forge.ParseRemote(string(out))
	if err != nil {
		return ForgeInfo{}, errf("invalid", "%v", err)
	}
	info := ForgeInfo{Repo: repo}
	if b, _, err := git(ctx, dir, nil, 4096, "symbolic-ref", "--quiet", "--short", "HEAD"); err == nil {
		info.Branch = strings.TrimSpace(string(b))
	}
	c, st := n.forgeClient()
	info.Authed = st.Token(repo.Host) != ""
	n.forges.mu.Lock()
	pr, ok := n.forges.probed[repo.Host]
	n.forges.mu.Unlock()
	if !ok || time.Since(pr.at) > 10*time.Minute {
		srv, err := c.Probe(ctx, repo.Host)
		if err != nil {
			return info, errh("unavailable", "`acpost forge login HOST --kind github|gitlab|forgejo` names it", "%v", err)
		}
		pr = probed{srv: srv, at: time.Now()}
		n.forges.mu.Lock()
		n.forges.probed[repo.Host] = pr
		n.forges.mu.Unlock()
	}
	info.Server, info.Repo.Kind = pr.srv, pr.srv.Kind
	return info, nil
}

func forgeErr(err error) error {
	if fe, ok := err.(*forge.Error); ok {
		code := "unavailable"
		switch {
		case fe.Status == 401 || fe.Status == 403:
			return errh("refused", "`acpost forge login HOST` keeps a token for it", "%s", fe.Message)
		case fe.Status == 404:
			code = "not_found"
		case fe.Status >= 400 && fe.Status < 500:
			code = "invalid"
		}
		return errf(code, "%s", fe.Message)
	}
	return err
}

// Forge is a repository's forge.
func (n *Node) Forge(ctx context.Context, p Principal, q ForgeQuery) (ForgeInfo, error) {
	return n.forgeOf(ctx, p, q)
}

// Pulls lists the pull requests of a repository's forge.
func (n *Node) Pulls(ctx context.Context, p Principal, q ForgeQuery, state string) ([]forge.Pull, error) {
	l, err := n.PullList(ctx, p, q, state)
	return l.Items, err
}

// Checks lists the CI runs of a ref (the repository's branch when none).
func (n *Node) Checks(ctx context.Context, p Principal, q ForgeQuery, ref string) ([]forge.Check, error) {
	l, err := n.CheckList(ctx, p, q, ref)
	return l.Items, err
}

// CILog reads a CI job's log, its secrets masked.
func (n *Node) CILog(ctx context.Context, p Principal, q ForgeQuery, job, target string) (string, error) {
	info, err := n.forgeOf(ctx, p, q)
	if err != nil {
		return "", err
	}
	c, _ := n.forgeClient()
	text, err := c.Log(ctx, info.Server, info.Repo, job, target)
	if err != nil {
		return "", forgeErr(err)
	}
	return redact(text), nil
}

// OpenPull opens a pull request from the repository's branch (unless head names another).
func (n *Node) OpenPull(ctx context.Context, p Principal, q ForgeQuery, r forge.PullRequest) (forge.Pull, error) {
	info, err := n.forgeOf(ctx, p, q)
	if err != nil {
		return forge.Pull{}, err
	}
	if r.Head == "" {
		r.Head = info.Branch
	}
	if r.Base == "" { // the repository's own (main, master, develop…), not a guess
		c, _ := n.forgeClient()
		if r.Base, err = c.DefaultBranch(ctx, info.Server, info.Repo); err != nil {
			return forge.Pull{}, forgeErr(err)
		}
	}
	what := fmt.Sprintf("Open a pull request on %s: %q (%s → %s)", info.Repo.Web, r.Title, r.Head, r.Base)
	if err := n.approve(ctx, p, what, r.Body, map[string]any{"forge": "open_pull", "repo": info.Repo.Web, "head": r.Head, "base": r.Base, "title": r.Title, "draft": r.Draft}); err != nil {
		return forge.Pull{}, err
	}
	c, _ := n.forgeClient()
	out, err := c.OpenPull(ctx, info.Server, info.Repo, r)
	return out, forgeErr(err)
}

// MergePull merges a pull request of the repository.
func (n *Node) MergePull(ctx context.Context, p Principal, q ForgeQuery, m forge.Merge) error {
	info, err := n.forgeOf(ctx, p, q)
	if err != nil {
		return err
	}
	what := fmt.Sprintf("Merge pull request #%d on %s (%s)", m.Number, info.Repo.Web, cmp.Or(m.Method, "merge"))
	if err := n.approve(ctx, p, what, "", map[string]any{"forge": "merge_pull", "repo": info.Repo.Web, "number": m.Number, "method": cmp.Or(m.Method, "merge"), "sha": m.SHA}); err != nil {
		return err
	}
	c, _ := n.forgeClient()
	return forgeErr(c.MergePull(ctx, info.Server, info.Repo, m))
}

// Options of an approval.
const (
	approveAllow  = "allow_once"
	approveReject = "reject_once"
)

// approve has a person approve what an agent is about to do on a forge (open or merge a pull
// request): a permission_request on the message the agent is working on, answered as any other
// (acpost approve, the app); unanswered within the agent's ask_timeout, refused. Whatever the
// agent's permissions say: even an agent that runs without asking (a cloud agent) asks for this.
// The node's user acts on its own.
func (n *Node) approve(ctx context.Context, p Principal, title, text string, input any) error {
	if p.Kind != "agent" {
		return nil
	}
	msgID := n.currentOf(p)
	if msgID == "" {
		return errh("refused", "an agent does this within a turn, where a person can approve it", "agent:%s has no turn running: nobody to approve %s", p.Name, title)
	}
	timeout := 10 * time.Minute
	if a := n.cfg.Agents[p.Name]; a != nil && a.AskTimeout > 0 {
		timeout = a.AskTimeout.D()
	}
	pending := n.askPending()
	req := pending.Open(acpx.Request{Kind: acpx.RequestPermission, Title: redact(title), Text: redact(text), ToolKind: "forge",
		RawInput: json.RawMessage(eventJSON(maskFields(input))),
		Options:  []acpx.Option{{ID: approveAllow, Name: "Allow", Kind: approveAllow}, {ID: approveReject, Name: "Reject", Kind: approveReject}}})
	n.asks.mu.Lock()
	n.asks.of[req.ID] = askRef{msg: msgID, req: req}
	n.asks.mu.Unlock()
	defer func() {
		n.asks.mu.Lock()
		delete(n.asks.of, req.ID)
		delete(n.asks.by, req.ID)
		n.asks.mu.Unlock()
	}()
	if err := n.addEvent(ctx, msgID, "permission_request", eventJSON(fitRequest(req)), true); err != nil {
		done, cancel := context.WithCancel(context.Background())
		cancel()
		pending.Wait(done, req.ID, 0) // what Open holds goes with it
		return errf("unavailable", "the approval could not be asked: %v", err)
	}
	reply, ok := pending.Wait(ctx, req.ID, timeout)
	decision, by := "timeout", ""
	if ok {
		by = n.answeredBy(req.ID)
		decision = "rejected"
		if reply.OptionID == approveAllow {
			decision = "allowed"
		}
	}
	_ = n.addEvent(n.ctx, msgID, "permission", eventJSON(map[string]any{"request_id": req.ID, "tool": req.Title, "decision": decision, "by": by}), true)
	if decision != "allowed" {
		return errh("refused", "a person approves it in the app or with `acpost approve`", "%s: %s", title, decision)
	}
	return nil
}

// PullList lists the pull requests of a repository's forge, and says whether it is cut.
func (n *Node) PullList(ctx context.Context, p Principal, q ForgeQuery, state string) (forge.PullList, error) {
	info, err := n.forgeOf(ctx, p, q)
	if err != nil {
		return forge.PullList{}, err
	}
	c, _ := n.forgeClient()
	out, err := c.PullList(ctx, info.Server, info.Repo, state)
	return out, forgeErr(err)
}

// CheckList lists the CI runs of a ref (the repository's branch when none), and says whether it
// is cut.
func (n *Node) CheckList(ctx context.Context, p Principal, q ForgeQuery, ref string) (forge.CheckList, error) {
	info, err := n.forgeOf(ctx, p, q)
	if err != nil {
		return forge.CheckList{}, err
	}
	if ref == "" {
		ref = info.Branch
	}
	c, _ := n.forgeClient()
	out, err := c.CheckList(ctx, info.Server, info.Repo, ref)
	return out, forgeErr(err)
}
