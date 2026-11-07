package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"github.com/k1dory/acpost/internal/keychain"
	"github.com/k1dory/acpost/internal/store"
)

// MCP servers the node's user gives the agents (the app's Settings): every ACP session an agent
// opens has those for it. Their environment's and headers' values are secrets, in the keychain
// (service acpost-mcp, account <server>/env/<NAME> or <server>/header/<NAME>).

type mcpSecrets struct {
	once sync.Once
	kc   *keychain.Store
	// mu: a change of a server (its secrets and its row) is one step for those reading them for a
	// session (an agent never gets a secret meant for another).
	mu sync.RWMutex
}

func (n *Node) mcpKeychain() *keychain.Store {
	n.mcpSec.once.Do(func() {
		home, _ := os.UserHomeDir()
		n.mcpSec.kc = keychain.Default("acpost-mcp", filepath.Join(home, ".config", "acpost"))
	})
	return n.mcpSec.kc
}

// mcpAccount is a secret's account: its server, the version of the server's secrets, where, name.
// A version no server refers to is dead: a value left there never reaches an agent.
func mcpAccount(server, ver, where, name string) string {
	if ver == "" {
		return server + "/" + where + "/" + name // kept before secrets had versions
	}
	return server + "/" + ver + "/" + where + "/" + name
}

// MCPServers lists the servers (their secrets' names, never values).
func (n *Node) MCPServers(ctx context.Context, p Principal) ([]store.MCPServer, error) {
	if err := userOnly(p, "MCP servers"); err != nil {
		return nil, err
	}
	out, err := n.st.MCPServers(ctx)
	if err != nil {
		return nil, errf("unavailable", "%v", err)
	}
	return out, nil
}

// PutMCPServer adds or changes a server; secrets are the values of its variables and headers
// (a name absent keeps the value kept; an empty value forgets it). The change is one step: its
// secrets are written as a new version, the server switched to it, the old version forgotten —
// a failure on the way leaves the server and its secrets as they were.
func (n *Node) PutMCPServer(ctx context.Context, p Principal, m store.MCPServer, secrets map[string]string) error {
	if err := userOnly(p, "MCP servers"); err != nil {
		return err
	}
	if err := m.Check(); err != nil {
		return errf("invalid", "%v", err)
	}
	kc := n.mcpKeychain()
	n.mcpSec.mu.Lock()
	defer n.mcpSec.mu.Unlock()
	var old *store.MCPServer
	if list, err := n.st.MCPServers(ctx); err == nil {
		for i := range list {
			if list[i].Name == m.Name {
				old = &list[i]
			}
		}
	} else {
		return errf("unavailable", "%v", err)
	}
	m.SecretVer = newSecretVer()
	var written []string
	undo := func() { n.forgetAccounts(written) }
	for where, names := range map[string][]string{"env": m.Env, "header": m.Headers} {
		for _, name := range names {
			v, given := secrets[where+"/"+name]
			if !given && old != nil {
				// Kept as it was: a store that cannot be read stops the change (nothing lost).
				kept, _, err := kc.Lookup(mcpAccount(old.Name, old.SecretVer, where, name))
				if err != nil {
					undo()
					return errf("unavailable", "%v", err)
				}
				v = kept
			}
			if v == "" {
				continue
			}
			acct := mcpAccount(m.Name, m.SecretVer, where, name)
			// Noted before it is written: a value of a version never published is forgotten
			// later even if this process stops here.
			if err := n.st.SetMCPOrphan(n.ctx, acct, false); err != nil {
				undo()
				return errf("unavailable", "%v", err)
			}
			written = append(written, acct)
			if err := kc.Set(acct, v); err != nil {
				undo()
				return errf("unavailable", "%v", err)
			}
		}
	}
	// The old version's secrets are noted to forget before the server leaves it: a note that
	// cannot be written stops the change (the sweep spares what is in use, so an early note is safe).
	if old != nil {
		for _, a := range versionAccounts(*old) {
			if err := n.st.SetMCPOrphan(n.ctx, a, false); err != nil {
				undo()
				return errf("unavailable", "%v", err)
			}
		}
	}
	if err := n.st.PutMCPServer(ctx, m); err != nil {
		undo()
		return errf("invalid", "%v", err)
	}
	// Published: the old version is what nothing refers to now (the new one's notes go at the
	// next sweep, which spares the versions in use).
	if old != nil {
		n.forgetSecrets(*old)
	} else {
		n.forgetAccounts(nil)
	}
	return nil
}

// newSecretVer is a version name nothing else has.
func newSecretVer() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// versionAccounts are the accounts of a version's secrets.
func versionAccounts(m store.MCPServer) []string {
	var accts []string
	for _, e := range m.Env {
		accts = append(accts, mcpAccount(m.Name, m.SecretVer, "env", e))
	}
	for _, h := range m.Headers {
		accts = append(accts, mcpAccount(m.Name, m.SecretVer, "header", h))
	}
	return accts
}

// forgetSecrets forgets a version's secrets.
func (n *Node) forgetSecrets(m store.MCPServer) { n.forgetAccounts(versionAccounts(m)) }

// forgetAccounts forgets secrets, and those noted to forget before, but for those of the versions
// servers use now. Each is noted first and the note dropped once it is gone, in the node's own
// context: a request that ends meanwhile leaves the note, and the next change forgets it.
func (n *Node) forgetAccounts(accts []string) {
	ctx := n.ctx
	for _, a := range accts {
		_ = n.st.SetMCPOrphan(ctx, a, false)
	}
	pending, _ := n.st.MCPOrphans(ctx)
	// Those given are tried whether or not their notes were written.
	seen := map[string]bool{}
	for _, a := range pending {
		seen[a] = true
	}
	for _, a := range accts {
		if !seen[a] {
			pending = append(pending, a)
		}
	}
	live := map[string]bool{}
	if list, err := n.st.MCPServers(ctx); err == nil {
		for _, m := range list {
			for _, e := range m.Env {
				live[mcpAccount(m.Name, m.SecretVer, "env", e)] = true
			}
			for _, h := range m.Headers {
				live[mcpAccount(m.Name, m.SecretVer, "header", h)] = true
			}
		}
	} else {
		return // what is in use is not known: nothing is forgotten
	}
	kc := n.mcpKeychain()
	for _, a := range pending {
		if live[a] {
			_ = n.st.SetMCPOrphan(ctx, a, true) // in use: not an orphan
			continue
		}
		if err := kc.Delete(a); err == nil {
			_ = n.st.SetMCPOrphan(ctx, a, true)
		}
	}
}

// DeleteMCPServer removes a server and its secrets.
func (n *Node) DeleteMCPServer(ctx context.Context, p Principal, name string) error {
	if err := userOnly(p, "MCP servers"); err != nil {
		return err
	}
	n.mcpSec.mu.Lock()
	defer n.mcpSec.mu.Unlock()
	list, err := n.st.MCPServers(ctx)
	if err != nil {
		return errf("unavailable", "%v", err)
	}
	for _, m := range list {
		if m.Name == name {
			for _, a := range versionAccounts(m) {
				if err := n.st.SetMCPOrphan(n.ctx, a, false); err != nil {
					return errf("unavailable", "%v", err)
				}
			}
		}
	}
	if err := n.st.DeleteMCPServer(ctx, name); err != nil {
		if errors.Is(err, store.ErrNoMCPServer) {
			return errf("not_found", "%v", err)
		}
		return errf("unavailable", "%v", err)
	}
	for _, m := range list {
		if m.Name == name {
			n.forgetSecrets(m)
		}
	}
	return nil
}

// mcpServers is what a session of the agent gets: the enabled servers for it, an HTTP one only
// when the agent takes HTTP.
func (a *acpAgent) mcpServers(ctx context.Context, p *acpProc) []acp.McpServer {
	out := []acp.McpServer{}
	a.n.mcpKeychain()
	a.n.mcpSec.mu.RLock()
	defer a.n.mcpSec.mu.RUnlock()
	list, err := a.n.st.MCPServers(ctx)
	if err != nil {
		return out
	}
	kc := a.n.mcpKeychain()
	for _, m := range list {
		if !m.Enabled || (len(m.Agents) > 0 && !slices.Contains(m.Agents, a.name)) {
			continue
		}
		switch m.Kind {
		case "stdio":
			env := []acp.EnvVariable{}
			for _, e := range m.Env {
				env = append(env, acp.EnvVariable{Name: e, Value: kc.Get(mcpAccount(m.Name, m.SecretVer, "env", e))})
			}
			args := m.Args
			if args == nil {
				args = []string{}
			}
			out = append(out, acp.McpServer{Stdio: &acp.McpServerStdio{Name: m.Name, Command: m.Command, Args: args, Env: env}})
		case "http":
			if p == nil || !p.init.AgentCapabilities.McpCapabilities.Http {
				continue
			}
			hs := []acp.HttpHeader{}
			for _, h := range m.Headers {
				hs = append(hs, acp.HttpHeader{Name: h, Value: kc.Get(mcpAccount(m.Name, m.SecretVer, "header", h))})
			}
			out = append(out, acp.McpServer{Http: &acp.McpServerHttpInline{Name: m.Name, Type: "http", Url: m.URL, Headers: hs}})
		}
	}
	return out
}
