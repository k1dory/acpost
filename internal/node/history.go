package node

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/k1dory/acpost/internal/store"
)

// A session's history, for the app to show a conversation started elsewhere (Claude Code, Codex
// in a terminal or an editor): the agent loads it (session/load) and replays it as updates, which
// are read as a turn's are — the user's messages, the agent's text and thinking, tool calls, plan —
// into events kept in memory, not in the store. Reading changes nothing; a message to the session
// (send --session) continues it.

const (
	historyEvents = 4000             // events of a history at most (the rest is cut, said in a notice)
	historyWait   = 30 * time.Second // to load a session
)

// SessionHistory replays an ACP session of an agent of this node: its turns as events, oldest first.
func (n *Node) SessionHistory(ctx context.Context, p Principal, agent, id, cwd string) ([]store.Event, error) {
	cfg := n.agentCfg(agent)
	a := n.acp[agent]
	if a == nil || cfg == nil {
		return nil, errh("not_found", "`acpost agents` lists them", "no ACP agent %s here", agent)
	}
	if err := canUseSessions(p, agent, cfg); err != nil {
		return nil, err
	}
	if id == "" || len(id) > 256 {
		return nil, errf("invalid", "a session id")
	}
	if cwd != "" {
		dir, err := checkDir(cwd)
		if err != nil {
			return nil, err
		}
		cwd = dir
	}
	// A process of its own: the replay touches no turn, no session another slot holds, and the
	// session is let go with the process (an agent that cannot close it holds it no longer).
	slot, done := a.spare()
	defer done()
	proc, err := slot.ensureProc(ctx)
	if err != nil {
		return nil, errf("unavailable", "agent:%s did not start: %v", agent, err)
	}
	if !proc.init.AgentCapabilities.LoadSession {
		return nil, errf("unsupported", "agent:%s cannot replay a session (no session/load)", agent)
	}
	if cwd == "" {
		cwd = slot.cwd()
	}
	sid := acp.SessionId(id)

	var mu sync.Mutex
	var out []store.Event
	cut := false
	t := &acpTurn{limit: 1 << 20, maxFiles: 0, maxBytes: 0, history: true}
	t.stream = &textStream{budget: historyEvents, record: func(kind string, data any) {
		mu.Lock()
		defer mu.Unlock()
		if len(out) >= historyEvents {
			cut = true
			return
		}
		out = append(out, store.Event{Seq: int64(len(out) + 1), Time: time.Now().UTC(), Kind: kind, Data: eventJSON(data)})
	}}
	proc.cl.capture(sid, t)
	lctx, cancel := context.WithTimeout(ctx, historyWait)
	_, err = proc.conn.LoadSession(lctx, acp.LoadSessionRequest{SessionId: sid, Cwd: cwd, McpServers: a.mcpServers(ctx, proc)})
	cancel()
	proc.cl.end(sid)
	if err != nil {
		return nil, explainACPErr(err, "session "+id)
	}
	slot.closeIfOneOff(proc, "", sid)
	mu.Lock()
	defer mu.Unlock()
	if cut {
		out = append(out, store.Event{Seq: int64(len(out) + 1), Time: time.Now().UTC(), Kind: "notice",
			Data: eventJSON(map[string]string{"text": "the history is longer: its first events are shown"})})
	}
	if out == nil {
		out = []store.Event{}
	}
	return out, nil
}

// capture registers a replay's pseudo-turn for a session (in a process of its own: no turn there).
func (c *acpClient) capture(sid acp.SessionId, t *acpTurn) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.turns[sid] = t
}

// spare is a slot outside the agent's slots, for one job in a process of its own (a replay, asking
// what the agent offers), with a token of its own and no turn; done stops its process and ends the
// token.
func (a *acpAgent) spare() (*acpSlot, func()) {
	s := &acpSlot{a: a, idx: -1, token: a.n.issueAgentToken(a.name)}
	return s, func() { s.stopProc(); a.n.revokeAgentToken(s.token) }
}

func explainACPErr(err error, what string) error {
	f := explainACP(err, what, true)
	return errh(f.Code, HintFor(f.Code), "%s", f.Message)
}

// sessionCwdApart is a session's directory as the agent lists it, asked of a spare process that is
// stopped (killed, if it must) after 30 s: an agent that stops reading holds no slot and no turn.
func (a *acpAgent) sessionCwdApart(ctx context.Context, sid string) string {
	slot, done := a.spare()
	defer done()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	stop := context.AfterFunc(ctx, slot.stopProc) // a write the agent does not read ends with it
	defer stop()
	proc, err := slot.ensureProc(ctx)
	if err != nil || proc.init.AgentCapabilities.SessionCapabilities.List == nil {
		return ""
	}
	cursor := ""
	for page := 0; page < maxSessionPages; page++ {
		req := acp.ListSessionsRequest{}
		if cursor != "" {
			req.Cursor = &cursor
		}
		resp, err := proc.conn.ListSessions(ctx, req)
		if err != nil {
			return ""
		}
		for _, si := range resp.Sessions {
			if string(si.SessionId) == sid {
				return si.Cwd
			}
		}
		if resp.NextCursor == nil || *resp.NextCursor == "" {
			return ""
		}
		cursor = *resp.NextCursor
	}
	return ""
}

// ForkSession forks an ACP session (the agent copies its conversation into a new one, in cwd or
// the session's own directory): the new session's id, a chat to go on in apart from the first.
func (n *Node) ForkSession(ctx context.Context, p Principal, agent, id, cwd string) (string, error) {
	cfg := n.agentCfg(agent)
	a := n.acp[agent]
	if a == nil || cfg == nil {
		return "", errh("not_found", "`acpost agents` lists them", "no ACP agent %s here", agent)
	}
	if err := canUseSessions(p, agent, cfg); err != nil {
		return "", err
	}
	if id == "" || len(id) > 256 {
		return "", errf("invalid", "a session id")
	}
	if cwd != "" {
		dir, err := checkDir(cwd)
		if err != nil {
			return "", err
		}
		cwd = dir
	} else {
		cwd = a.sessionCwdApart(ctx, id)
	}
	slot, done := a.spare()
	defer done()
	if cwd == "" {
		cwd = slot.cwd()
	}
	ctx, cancel := context.WithTimeout(ctx, historyWait)
	defer cancel()
	stop := context.AfterFunc(ctx, slot.stopProc)
	defer stop()
	proc, err := slot.ensureProc(ctx)
	if err != nil {
		return "", errf("unavailable", "agent:%s did not start: %v", agent, err)
	}
	if proc.init.AgentCapabilities.SessionCapabilities.Fork == nil {
		return "", errf("unsupported", "agent:%s cannot fork a session (no session/fork)", agent)
	}
	// The unstable request has its own copy of the MCP server types: the same JSON.
	var mcp []acp.UnstableMcpServer
	if raw, err := json.Marshal(a.mcpServers(ctx, proc)); err == nil {
		_ = json.Unmarshal(raw, &mcp)
	}
	resp, err := proc.conn.UnstableForkSession(ctx, acp.UnstableForkSessionRequest{SessionId: acp.SessionId(id), Cwd: cwd, McpServers: mcp})
	if err != nil {
		return "", explainACPErr(err, "agent:"+agent+" could not fork the session")
	}
	return string(resp.SessionId), nil
}
