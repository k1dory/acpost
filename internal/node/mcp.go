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
	undo := func() {
		for _, acct := range written {
			_ = kc.Delete(acct)
		}
	}
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
			if err := kc.Set(acct, v); err != nil {
				undo()
				return errf("unavailable", "%v", err)
			}
			written = append(written, acct)
		}
	}
	if err := n.st.PutMCPServer(ctx, m); err != nil {
		undo()
		return errf("invalid", "%v", err)
	}
	if old != nil {
		n.forgetSecrets(ctx, *old) // the old version: nothing refers to it any more
	}
	return nil
}

// newSecretVer is a version name nothing else has.
func newSecretVer() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// forgetSecrets forgets a version's secrets (and those that could not be forgotten before); what it
// cannot forget now is noted, to be forgotten at the next change.
func (n *Node) forgetSecrets(ctx context.Context, m store.MCPServer) {
	kc := n.mcpKeychain()
	accts, _ := n.st.MCPOrphans(ctx)
	for _, e := range m.Env {
		accts = append(accts, mcpAccount(m.Name, m.SecretVer, "env", e))
	}
	for _, h := range m.Headers {
		accts = append(accts, mcpAccount(m.Name, m.SecretVer, "header", h))
	}
	for _, a := range accts {
		err := kc.Delete(a)
		_ = n.st.SetMCPOrphan(ctx, a, err == nil)
	}
}

// DeleteMCPServer removes a server and its secrets.
func (n *Node) DeleteMCPServer(ctx context.Context, p Principal, name string) error {
	if err := userOnly(p, "MCP servers"); err != nil {
		return err
	}
	n.mcpSec.mu.Lock()
	defer n.mcpSec.mu.Unlock()
	list, _ := n.st.MCPServers(ctx)
	if err := n.st.DeleteMCPServer(ctx, name); err != nil {
		if errors.Is(err, store.ErrNoMCPServer) {
			return errf("not_found", "%v", err)
		}
		return errf("unavailable", "%v", err)
	}
	for _, m := range list {
		if m.Name == name {
			n.forgetSecrets(ctx, m)
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
