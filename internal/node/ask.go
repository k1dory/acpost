package node

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	acp "github.com/coder/acp-go-sdk"
	"github.com/k1dory/acpx"
)

// permissions: ask — an agent's request for permission waits for a person: it is a
// permission_request event of the message (the tool call, what it would run, the agent's
// options), answered by the node's user or the message's sender (POST …/permission, acpost
// approve); unanswered within ask_timeout, it is refused. The answer is a permission event.

type asks struct {
	once    sync.Once
	pending *acpx.Pending
	mu      sync.Mutex
	of      map[string]askRef // request id → its message
	by      map[string]string // request id → who answered it
}

type askRef struct {
	msg string
	req acpx.Request
}

func (n *Node) askPending() *acpx.Pending {
	n.asks.once.Do(func() {
		n.asks.pending = acpx.NewPending()
		n.asks.of = map[string]askRef{}
		n.asks.by = map[string]string{}
	})
	return n.asks.pending
}

// ask holds the agent's request until a person answers, or ask_timeout passes (refused then).
func (c *acpClient) ask(ctx context.Context, t *acpTurn, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	n := c.slot.a.n
	pending := n.askPending()
	req := acpx.PermissionRequest(p)
	if req.RawInput != nil { // what it would run, its secrets masked
		var v any
		if json.Unmarshal(req.RawInput, &v) == nil {
			req.RawInput = json.RawMessage(eventJSON(maskFields(v)))
		}
	}
	req.Text = redact(req.Text)
	req = pending.Open(req) // whole: what an answer is checked against and the agent gets back
	n.asks.mu.Lock()
	n.asks.of[req.ID] = askRef{msg: t.msgID, req: req}
	n.asks.mu.Unlock()
	defer func() {
		n.asks.mu.Lock()
		delete(n.asks.of, req.ID)
		delete(n.asks.by, req.ID)
		n.asks.mu.Unlock()
	}()
	c.event(t, "permission_request", fitRequest(req)) // its event: bounded

	// The wait ends with the turn too (its timeout, its message cancelled): the connection's ctx
	// would not, and the agent waits for this answer before it ends the turn.
	t.mu.Lock()
	turnCtx := t.ctx
	t.mu.Unlock()
	wctx, stop := context.WithCancel(ctx)
	defer stop()
	if turnCtx != nil {
		defer context.AfterFunc(turnCtx, stop)()
	}
	reply, ok := pending.Wait(wctx, req.ID, c.slot.a.cfg.AskTimeout.D())
	decision, by := "", reply.Action
	var out acp.RequestPermissionResponse
	if ok {
		resp, kind, err := acpx.PermissionResponse(req, reply)
		out, decision = resp, kind
		if err != nil {
			decision = "cancelled"
		}
		by = n.answeredBy(req.ID)
	} else {
		// Nobody answered in time (or the turn ended): refused, as deny would.
		out, decision, by = refuse(p), "timeout", ""
	}
	c.event(t, "permission", map[string]any{"request_id": req.ID, "tool": req.Title, "id": p.ToolCall.ToolCallId, "decision": decision, "by": by})
	return out, nil
}

// refuse picks the agent's own reject option, or cancels.
func refuse(p acp.RequestPermissionRequest) acp.RequestPermissionResponse {
	for _, k := range []acp.PermissionOptionKind{acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways} {
		for _, o := range p.Options {
			if o.Kind == k {
				return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(o.OptionId)}
			}
		}
	}
	return acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}
}

// Answer answers a permission request of a message: an option it offered, or "decline".
func (n *Node) Answer(ctx context.Context, p Principal, msgID string, reply acpx.Reply) error {
	m, err := n.Get(ctx, p, msgID)
	if err != nil {
		return err
	}
	// The node's user, the message's sender, or the node it came from (for a sender there).
	fromNode := p.Kind == "node" && strings.HasSuffix(m.From, "@"+p.Name)
	if p.Kind != "user" && m.From != p.Address() && !fromNode {
		return errf("refused", "the node's user or the message's sender answers its permission requests")
	}
	if m.State.Final() {
		return errf("conflict", "%s is %s: its requests are closed", msgID, m.State)
	}
	n.askPending()
	n.asks.mu.Lock()
	ref, ok := n.asks.of[reply.RequestID]
	n.asks.mu.Unlock()
	if !ok || ref.msg != msgID {
		return errh("not_found", "`acpost show ID --events` shows the permission requests still open", "no permission request %s waits on %s", reply.RequestID, msgID)
	}
	if reply.OptionID == "" && reply.Action == "" {
		reply.Action = acpx.ActionDecline
	}
	if reply.OptionID != "" && !strings.Contains(optionIDs(ref.req), "\x00"+reply.OptionID+"\x00") {
		return errf("invalid", "option %q is not one the request offers (%s)", reply.OptionID, strings.Trim(strings.ReplaceAll(optionIDs(ref.req), "\x00\x00", ", "), "\x00"))
	}
	// What an agent does on a forge (open, merge a pull request) is a person's to approve: the
	// node's user, never an agent that sent the work (or itself) to the agent asking.
	if ref.req.ToolKind == "forge" && p.Kind != "user" {
		return errh("refused", "the node's user approves it (the app, `acpost approve` on that node)", "%s is for a person to approve", ref.req.Title)
	}
	// Who answered is recorded with the answer, at once: of two answers at the same time, the one
	// taken is the one named (the waiter reads it under the same lock, after the answer).
	n.asks.mu.Lock()
	defer n.asks.mu.Unlock()
	if err := n.askPending().Resolve(reply); err != nil {
		return errf("conflict", "the request was answered already, or timed out")
	}
	n.asks.by[reply.RequestID] = p.Address()
	return nil
}

// answeredBy is who answered a request (taken once: the record goes).
func (n *Node) answeredBy(id string) string {
	n.asks.mu.Lock()
	defer n.asks.mu.Unlock()
	by := n.asks.by[id]
	delete(n.asks.by, id)
	return by
}

func optionIDs(r acpx.Request) string {
	var b strings.Builder
	for _, o := range r.Options {
		b.WriteString("\x00" + o.ID + "\x00")
	}
	return b.String()
}

// Asking lists the permission requests waiting for a person (the node's user sees all).
func (n *Node) Asking(p Principal) ([]AskingRequest, error) {
	if p.Kind != "user" {
		return nil, errf("refused", "the node's user lists the permission requests")
	}
	n.askPending()
	n.asks.mu.Lock()
	defer n.asks.mu.Unlock()
	out := make([]AskingRequest, 0, len(n.asks.of))
	for _, r := range n.asks.of {
		out = append(out, AskingRequest{Message: r.msg, Request: r.req})
	}
	return out, nil
}

// AskingRequest is a permission request waiting, with its message.
type AskingRequest struct {
	Message string `json:"message"`
	acpx.Request
}

// fitRequest keeps a request's event within the store's event size, readable: its id whole, its
// options (16 at most, names and ids cut), what it would run and its text cut from what they were
// (4 KiB each, then halving, a bounded number of times; at worst gone).
func fitRequest(r acpx.Request) acpx.Request {
	r.Title, r.ToolCallID, r.ToolKind = cutString(r.Title, 512), cutString(r.ToolCallID, 128), cutString(r.ToolKind, 64)
	r.Options = append([]acpx.Option(nil), r.Options[:min(len(r.Options), 16)]...) // a copy: the request keeps its own
	for i := range r.Options {
		o := &r.Options[i]
		o.ID, o.Name, o.Kind = cutString(o.ID, 128), cutString(o.Name, 128), cutString(o.Kind, 64)
	}
	text, raw := r.Text, string(r.RawInput)
	tn, rn := min(len(text), 4<<10), min(len(raw), 4<<10)
	for range 32 {
		r.Text = cutString(text, tn)
		r.RawInput = nil
		if rn > 0 {
			if rn >= len(raw) {
				r.RawInput = json.RawMessage(raw)
			} else {
				r.RawInput = json.RawMessage(eventJSON(cutString(raw, rn) + "…"))
			}
		}
		if len(eventJSON(r)) <= toolEventMax {
			return r
		}
		tn, rn = tn/2, rn/2
	}
	r.Text, r.RawInput = "", nil
	return r
}
