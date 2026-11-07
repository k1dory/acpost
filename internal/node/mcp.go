package node

import (
	"context"
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

func mcpAccount(server, where, name string) string { return server + "/" + where + "/" + name }

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
// (a name absent keeps the value kept; an empty value forgets it).
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
	// The names the server no longer has: their values go.
	if list, err := n.st.MCPServers(ctx); err == nil {
		for _, old := range list {
			if old.Name != m.Name {
				continue
			}
			for where, names := range map[string][]string{"env": old.Env, "header": old.Headers} {
				keep := m.Env
				if where == "header" {
					keep = m.Headers
				}
				for _, name := range names {
					if !slices.Contains(keep, name) {
						_ = kc.Delete(mcpAccount(m.Name, where, name))
					}
				}
			}
		}
	}
	for where, names := range map[string][]string{"env": m.Env, "header": m.Headers} {
		for _, name := range names {
			v, given := secrets[where+"/"+name]
			if !given {
				continue
			}
			acct := mcpAccount(m.Name, where, name)
			if v == "" {
				_ = kc.Delete(acct)
			} else if err := kc.Set(acct, v); err != nil {
				return errf("unavailable", "%v", err)
			}
		}
	}
	if err := n.st.PutMCPServer(ctx, m); err != nil {
		return errf("invalid", "%v", err)
	}
	return nil
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
	kc := n.mcpKeychain()
	for _, m := range list {
		if m.Name != name {
			continue
		}
		for _, e := range m.Env {
			_ = kc.Delete(mcpAccount(name, "env", e))
		}
		for _, h := range m.Headers {
			_ = kc.Delete(mcpAccount(name, "header", h))
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
				env = append(env, acp.EnvVariable{Name: e, Value: kc.Get(mcpAccount(m.Name, "env", e))})
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
				hs = append(hs, acp.HttpHeader{Name: h, Value: kc.Get(mcpAccount(m.Name, "header", h))})
			}
			out = append(out, acp.McpServer{Http: &acp.McpServerHttpInline{Name: m.Name, Type: "http", Url: m.URL, Headers: hs}})
		}
	}
	return out
}
