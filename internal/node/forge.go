package node

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/k1dory/acpost/internal/forge"
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
		home, _ := os.UserHomeDir()
		st := &forge.Store{Dir: filepath.Join(home, ".config", "acpost")}
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
	info, err := n.forgeOf(ctx, p, q)
	if err != nil {
		return nil, err
	}
	c, _ := n.forgeClient()
	out, err := c.Pulls(ctx, info.Server, info.Repo, state)
	return out, forgeErr(err)
}

// Checks lists the CI runs of a ref (the repository's branch when none).
func (n *Node) Checks(ctx context.Context, p Principal, q ForgeQuery, ref string) ([]forge.Check, error) {
	info, err := n.forgeOf(ctx, p, q)
	if err != nil {
		return nil, err
	}
	if ref == "" {
		ref = info.Branch
	}
	c, _ := n.forgeClient()
	out, err := c.Checks(ctx, info.Server, info.Repo, ref)
	return out, forgeErr(err)
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
	c, _ := n.forgeClient()
	out, err := c.OpenPull(ctx, info.Server, info.Repo, r)
	return out, forgeErr(err)
}
