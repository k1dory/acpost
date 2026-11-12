package node

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// Traces: where agents' time went, turn by turn. The node gives each turn's times and the timing
// of its events (what a call was, when it started and ended, when a permission was asked and
// answered), without what the events carried; the app makes the timeline and the sums of them.

const (
	tracesDefault = 20     // conversations listed
	tracesMax     = 50     // at most
	traceOwn      = 100    // a conversation's last turns
	traceUnder    = 400    // turns of the agents they asked
	traceBudget   = 60_000 // events in one answer
	traceText     = 160    // characters of a call's title or command
)

// Traces are the most recently active conversations people started (limit of them), or the one
// thread, as traces read them. The node's user and its agents only: they span every agent.
func (n *Node) Traces(ctx context.Context, p Principal, thread string, limit int) ([]client.TraceThread, error) {
	if !p.local() {
		return nil, errf("refused", "traces are for the node's user")
	}
	var threads []string
	if thread != "" {
		threads = []string{thread}
	} else {
		if limit <= 0 {
			limit = tracesDefault
		}
		var err error
		if threads, err = n.st.TraceThreads(ctx, min(limit, tracesMax)); err != nil {
			return nil, errf("unavailable", "%v", err)
		}
	}
	titles, _ := n.st.ChatTitles(ctx)
	budget := traceBudget
	out := make([]client.TraceThread, 0, len(threads))
	for _, th := range threads {
		msgs, cut, err := n.st.TraceMessages(ctx, th, traceOwn, traceUnder)
		if err != nil {
			return nil, errf("unavailable", "%v", err)
		}
		if len(msgs) == 0 {
			if thread != "" {
				return nil, errf("not_found", "no conversation %s", thread)
			}
			continue
		}
		t := client.TraceThread{Thread: th, Title: titles["thread:"+th], Messages: make([]client.TraceMessage, 0, len(msgs)), Cut: cut}
		if t.Title == "" {
			if root, err := n.st.Get(ctx, th); err == nil {
				t.Title = clipLine(root.Body)
			}
		}
		for _, m := range msgs {
			if m.Thread == th && m.UpdatedAt.After(t.Updated) {
				t.Updated = m.UpdatedAt
			}
			evs, cut, err := n.traceEvents(ctx, m.ID, &budget)
			if err != nil {
				return nil, errf("unavailable", "%v", err)
			}
			t.Cut = t.Cut || cut
			t.Messages = append(t.Messages, client.TraceMessage{ID: m.ID, Thread: m.Thread, Parent: m.Parent, Depth: m.Depth,
				From: m.From, To: m.To, State: m.State, ErrorCode: m.ErrorCode,
				Created: m.CreatedAt, Updated: m.UpdatedAt, Finished: m.FinishedAt, Events: evs, Cut: cut})
		}
		out = append(out, t)
	}
	return out, nil
}

// traceEvents are a message's events as a trace reads them, while budget lasts (cut: it ran out).
func (n *Node) traceEvents(ctx context.Context, id string, budget *int) ([]client.TraceEvent, bool, error) {
	var tr tracer
	out := []client.TraceEvent{}
	var after int64
	for {
		page, err := n.st.Events(ctx, id, after, store.MaxPage)
		if err != nil {
			return nil, false, err
		}
		for _, e := range page {
			if *budget <= 0 {
				return out, true, nil
			}
			var kept bool
			out, kept = tr.add(out, e)
			if kept {
				*budget--
			}
		}
		if len(page) < store.MaxPage {
			return out, false, nil
		}
		after = page[len(page)-1].Seq
	}
}

// tracer reads a message's events in order: what each call last said, so an update saying
// nothing new is left out.
type tracer struct {
	calls map[string]*traced
}

type traced struct{ status, title, command string }

// add appends what a trace needs of e to out (kept: an event was added, not one extended).
func (tr *tracer) add(out []client.TraceEvent, e store.Event) ([]client.TraceEvent, bool) {
	at := e.Time.UnixMilli()
	switch e.Kind {
	case "text", "thought":
		// A run of chunks is one span: the last one's end is moved.
		if l := len(out) - 1; l >= 0 && out[l].Kind == e.Kind {
			out[l].End = at
			return out, false
		}
		return append(out, client.TraceEvent{Seq: e.Seq, At: at, End: at, Kind: e.Kind}), true
	case "tool_call", "tool_call_update":
		var d struct {
			ID     string          `json:"id"`
			Status string          `json:"status"`
			Kind   string          `json:"kind"`
			Title  string          `json:"title"`
			Input  json.RawMessage `json:"input"`
			Exit   *int            `json:"exit_code"`
		}
		if json.Unmarshal([]byte(e.Data), &d) != nil || d.ID == "" {
			return out, false
		}
		if tr.calls == nil {
			tr.calls = map[string]*traced{}
		}
		c := tr.calls[d.ID]
		first := c == nil
		if first {
			c = &traced{}
			tr.calls[d.ID] = c
		}
		ev := client.TraceEvent{Seq: e.Seq, At: at, Kind: e.Kind, Call: d.ID, Tool: d.Kind, Exit: d.Exit}
		news := first || d.Exit != nil
		if d.Status != "" && d.Status != c.status {
			ev.Status, c.status, news = d.Status, d.Status, true
		}
		if t := clipLine(d.Title); t != "" && t != c.title {
			ev.Title, c.title, news = t, t, true
		}
		if cmd := clipLine(commandOf(d.Input)); cmd != "" && cmd != c.command {
			ev.Command, c.command, news = cmd, cmd, true
		}
		if !news {
			return out, false
		}
		return append(out, ev), true
	case "permission_request":
		var d struct {
			RequestID string `json:"request_id"`
			Call      string `json:"tool_call_id"`
			Title     string `json:"title"`
			ToolKind  string `json:"tool_kind"`
		}
		if json.Unmarshal([]byte(e.Data), &d) != nil || d.RequestID == "" {
			return out, false
		}
		return append(out, client.TraceEvent{Seq: e.Seq, At: at, Kind: e.Kind, Request: d.RequestID, Call: d.Call, Title: clipLine(d.Title), Tool: d.ToolKind}), true
	case "permission":
		var d struct {
			RequestID string `json:"request_id"`
			Decision  string `json:"decision"`
		}
		if json.Unmarshal([]byte(e.Data), &d) != nil || d.RequestID == "" {
			return out, false
		}
		return append(out, client.TraceEvent{Seq: e.Seq, At: at, Kind: e.Kind, Request: d.RequestID, Status: d.Decision}), true
	case "notice":
		var d struct {
			Severity string `json:"severity"`
		}
		_ = json.Unmarshal([]byte(e.Data), &d)
		return append(out, client.TraceEvent{Seq: e.Seq, At: at, Kind: e.Kind, Status: d.Severity}), true
	case "truncated":
		return append(out, client.TraceEvent{Seq: e.Seq, At: at, Kind: e.Kind}), true
	}
	return out, false // usage, the plan, changes: not of time
}

// commandOf is the shell command a call's input names: a string, or the words of one.
func commandOf(raw json.RawMessage) string {
	var in struct {
		Command json.RawMessage `json:"command"`
	}
	if len(raw) == 0 || json.Unmarshal(raw, &in) != nil || len(in.Command) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(in.Command, &s) == nil {
		return s
	}
	var words []string
	if json.Unmarshal(in.Command, &words) == nil {
		return strings.Join(words, " ")
	}
	return ""
}

// clipLine is a text's first line, at most traceText characters.
func clipLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	s = strings.ToValidUTF8(s, "")
	if r := []rune(s); len(r) > traceText {
		return string(r[:traceText]) + "…"
	}
	return s
}
