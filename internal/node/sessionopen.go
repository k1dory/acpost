package node

import (
	"context"
	"encoding/json"
	"net/url"
	"strings"

	acp "github.com/coder/acp-go-sdk"

	"github.com/k1dory/acpost/internal/store"
)

// What a sender gives the session its message opens, beside the agent's own configuration: text
// appended to the agent's system prompt, and MCP servers of its own. A backend that runs chats on
// the node (each with its rules and its own tools) says them here; the node's MCP servers (PUT
// /v1/mcp) are every session's.
//
// They are the session's from when it is opened (session/new, session/load, session/resume): a
// message into a session already open in the agent's process does not change them. An agent takes
// the system prompt as `_meta.systemPrompt` ({"append": …}: claude-agent-acp) or leaves it.
const (
	metaSystem = "acp_system"
	metaMCP    = "acp_mcp"

	systemMax     = 32 << 10
	sessionMCPMax = 8
)

// SessionMCP is an HTTP MCP server given to the session a message opens.
type SessionMCP struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// checkSessionOpen: the system text within its bound and without NULs; the servers named as the
// node's own are, each an http(s) URL, no name twice.
func checkSessionOpen(system string, mcp []SessionMCP) error {
	if len(system) > systemMax {
		return errf("refused", "system is %d bytes, over %d", len(system), systemMax)
	}
	if strings.ContainsRune(system, 0) {
		return errf("invalid", "system: text")
	}
	if len(mcp) > sessionMCPMax {
		return errf("invalid", "mcp: at most %d servers with a message", sessionMCPMax)
	}
	seen := map[string]bool{}
	for _, m := range mcp {
		if err := store.CheckMCPName(m.Name); err != nil {
			return errf("invalid", "mcp: %v", err)
		}
		if seen[m.Name] {
			return errf("invalid", "mcp: %s twice", m.Name)
		}
		seen[m.Name] = true
		u, err := url.Parse(m.URL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(m.URL) > 2048 {
			return errf("invalid", "mcp %s: an http(s) URL of at most 2048 bytes", m.Name)
		}
	}
	return nil
}

type sessionOpenKey struct{}

// sessionOpen is what the message's sender gave its session.
type sessionOpen struct {
	system string
	mcp    []SessionMCP
}

// withSessionOpen carries the message's system text and MCP servers to where its session is
// opened.
func withSessionOpen(ctx context.Context, m store.Message) context.Context {
	o := sessionOpen{system: m.Meta[metaSystem]}
	if raw := m.Meta[metaMCP]; raw != "" {
		_ = json.Unmarshal([]byte(raw), &o.mcp)
	}
	if o.system == "" && len(o.mcp) == 0 {
		return ctx
	}
	return context.WithValue(ctx, sessionOpenKey{}, o)
}

// openMeta is the `_meta` of a session's opening request: the sender's system text, appended to
// the agent's own prompt.
func openMeta(ctx context.Context) map[string]any {
	o, _ := ctx.Value(sessionOpenKey{}).(sessionOpen)
	if o.system == "" {
		return nil
	}
	return map[string]any{"systemPrompt": map[string]any{"append": o.system}}
}

// openMCP is the MCP servers of a session being opened: the node's for the agent, then the
// sender's (an HTTP one only when the agent takes HTTP; one named as a node's server stands in
// its place — the sender's is the nearer choice).
func (a *acpAgent) openMCP(ctx context.Context, p *acpProc) []acp.McpServer {
	out := a.mcpServers(ctx, p)
	o, _ := ctx.Value(sessionOpenKey{}).(sessionOpen)
	if len(o.mcp) == 0 || p == nil || !p.init.AgentCapabilities.McpCapabilities.Http {
		return out
	}
	for _, m := range o.mcp {
		srv := acp.McpServer{Http: &acp.McpServerHttpInline{Name: m.Name, Type: "http", Url: m.URL, Headers: []acp.HttpHeader{}}}
		replaced := false
		for i, have := range out {
			if (have.Http != nil && have.Http.Name == m.Name) || (have.Stdio != nil && have.Stdio.Name == m.Name) {
				out[i], replaced = srv, true
			}
		}
		if !replaced {
			out = append(out, srv)
		}
	}
	return out
}
