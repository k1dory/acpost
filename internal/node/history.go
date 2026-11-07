package node

import (
	"context"
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
	slot := a.slots[0]
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
	if !proc.cl.capture(sid, t) {
		return nil, errf("conflict", "session %s is in a turn here: its history is in that turn's events", id)
	}
	lctx, cancel := context.WithTimeout(ctx, historyWait)
	_, err = proc.conn.LoadSession(lctx, acp.LoadSessionRequest{SessionId: sid, Cwd: cwd, McpServers: []acp.McpServer{}})
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

// capture registers a replay's pseudo-turn for a session, unless a turn of it runs.
func (c *acpClient) capture(sid acp.SessionId, t *acpTurn) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, busy := c.turns[sid]; busy {
		return false
	}
	c.turns[sid] = t
	return true
}

func explainACPErr(err error, what string) error {
	f := explainACP(err, what, true)
	return errh(f.Code, HintFor(f.Code), "%s", f.Message)
}
