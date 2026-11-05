package node

// Regression tests from the second Codex review (gpt-6-astra, 2026-11-06): sessions and errors.

import (
	"context"
	"fmt"
	"net"
	"os"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/k1dory/acpx"

	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

func TestSendOnlyClientCannotAttachOthersSession(t *testing.T) {
	n, _ := sessionsNode(t)
	private := ask(t, n, SendRequest{Body: "count", Session: "older"})
	p := Principal{
		Kind: "client", Name: "ci",
		Token: &config.Token{SendTo: []string{"agent:fake"}},
	}

	if _, err := n.Get(ctx, p, private.ID); code(err) != "not_found" {
		t.Fatalf("private message unexpectedly readable: %v", err)
	}
	_, _, err := n.Send(ctx, p, SendRequest{
		To: "fake", Body: "count", Session: "older",
	})
	if code(err) != "refused" {
		t.Fatalf("attached to another sender's session: %v", err)
	}
}

func TestExplicitSessionAliasPreservesStateAcrossSlots(t *testing.T) {
	n := acpNode(t, "preamble: false\nconcurrency: 2")
	var original acp.SessionId

	for i, index := range []int{0, 1, 0} {
		slot := n.acp["fake"].slots[index]
		p, err := slot.ensureProc(ctx)
		if err != nil {
			t.Fatal(err)
		}
		key := "task:T"
		if i == 1 {
			key = "session:" + string(original)
		}
		sid, err := slot.session(ctx, p, key)
		if err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			original = sid
		} else if sid != original {
			t.Fatalf("changed session: %q != %q", sid, original)
		}

		turn := p.cl.begin(sid, "alias-regression", 1024)
		_, err = p.conn.Prompt(ctx, acp.PromptRequest{
			SessionId: sid,
			Prompt:    []acp.ContentBlock{acp.TextBlock("count")},
		})
		p.cl.end(sid)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := turn.reply("last"), fmt.Sprint(i+1); got != want {
			t.Fatalf("slot %d: got %q, want %q", index, got, want)
		}
	}
}

func TestSessionsHonorsAllowFrom(t *testing.T) {
	n, _ := sessionsNode(t)
	n.cfg.Agents["fake"].AllowFrom = []string{me.Address()}
	p := Principal{
		Kind: "client", Name: "ci",
		Token: &config.Token{SendTo: []string{"agent:fake"}},
	}

	if _, _, err := n.Send(ctx, p, SendRequest{
		To: "fake", Body: "x",
	}); code(err) != "refused" {
		t.Fatalf("send should be refused: %v", err)
	}
	list, _, err := n.Sessions(ctx, p, "fake", "", "")
	if code(err) != "refused" {
		t.Fatalf("disclosed sessions despite allow_from: %+v, %v", list, err)
	}
}

func TestSendRejectsForgedSessionMetadata(t *testing.T) {
	for _, tc := range []struct {
		name    string
		session string
		meta    map[string]string
	}{
		{"session", "", map[string]string{"acp_session": "older"}},
		{"cwd", "older", map[string]string{"acp_session_cwd": "/untrusted"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			n, _ := sessionsNode(t)
			_, _, err := n.Send(ctx, me, SendRequest{
				To: "fake", Body: "count",
				Session: tc.session, Meta: tc.meta,
			})
			if code(err) != "invalid" && code(err) != "refused" {
				t.Fatalf("accepted forged control metadata: %v", err)
			}
		})
	}
}

func TestExplicitSessionUsesListedCwd(t *testing.T) {
	loaded := make(chan string, 1)
	n, slot, p := reviewSessionNode(t, &reviewSessionAgent{
		list: func(acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
			return acp.ListSessionsResponse{
				Sessions: []acp.SessionInfo{{SessionId: "editor", Cwd: "/editor"}},
			}, nil
		},
		load: func(r acp.LoadSessionRequest) (acp.LoadSessionResponse, error) {
			loaded <- r.Cwd
			return acp.LoadSessionResponse{}, nil
		},
	})
	list, _, err := n.Sessions(ctx, me, "fake", "", "")
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := n.Send(ctx, me, SendRequest{
		To: "fake", Body: "work", Session: list[0].ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = slot.sessionIn(ctx, p,
		sessionKey(slot.a.cfg.Session, m), m.Meta[metaSessionCwd])
	if err != nil {
		t.Fatal(err)
	}
	if got := <-loaded; got != "/editor" {
		t.Fatalf("loaded with cwd %q, want /editor", got)
	}
}

func TestLatestSearchesAllSessionPages(t *testing.T) {
	n, _, _ := reviewSessionNode(t, &reviewSessionAgent{
		list: func(r acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
			if r.Cursor == nil {
				return acp.ListSessionsResponse{
					Sessions: []acp.SessionInfo{{
						SessionId: "older", Cwd: "/project",
						UpdatedAt: acp.Ptr("2026-11-05T00:00:00Z"),
					}},
					NextCursor: acp.Ptr("page-2"),
				}, nil
			}
			return acp.ListSessionsResponse{
				Sessions: []acp.SessionInfo{{
					SessionId: "newer", Cwd: "/project",
					UpdatedAt: acp.Ptr("2026-11-06T00:00:00Z"),
				}},
			}, nil
		},
	})
	m, _, err := n.Send(ctx, me, SendRequest{
		To: "fake", Body: "work", Session: "latest",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := m.Meta[metaSession]; got != "newer" {
		t.Fatalf("latest selected %q", got)
	}
}

func TestLatestRetryReturnsOriginalAfterSessionDeletion(t *testing.T) {
	n, state := sessionsNode(t)
	req := SendRequest{
		To: "fake", Body: "count", Session: "latest", IdemKey: "retry",
	}
	first := ask(t, n, req)
	for _, id := range []string{"older", "newer"} {
		if err := os.Remove(state + "/" + id); err != nil {
			t.Fatal(err)
		}
	}

	again, created, err := n.Send(ctx, me, req)
	if err != nil || created || again.ID != first.ID {
		t.Fatalf("lost idempotent result: id=%q created=%v err=%v",
			again.ID, created, err)
	}
}

func TestSameTaskSelfSendRefusedWithTwoSlots(t *testing.T) {
	n, slot, _ := reviewSessionNode(t, &reviewSessionAgent{})
	_, _, err := n.Send(ctx, me, SendRequest{
		To: "fake", Body: "parent", Task: "T",
	})
	if err != nil {
		t.Fatal(err)
	}
	parent, ok, err := n.st.Claim(ctx, "agent:fake", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}

	unlock := slot.a.lockKey("task:T")
	defer unlock()
	n.setCurrent(slot.token, parent.ID)
	p, _ := n.PrincipalForToken(slot.token)

	child, _, err := n.Send(ctx, p, SendRequest{To: "fake", Body: "child"})
	if code(err) != "refused" {
		t.Fatalf("accepted child blocked by parent's session: task=%q err=%v",
			child.Task, err)
	}
}

func TestNamedSessionInternalErrorIsNotNotFound(t *testing.T) {
	f := explainACP(&acp.RequestError{
		Code: -32603, Message: "session database temporarily unavailable",
	}, "load session saved", true)

	if f.Code != "agent_error" {
		t.Fatalf("misleading failure: code=%q message=%q hint=%q",
			f.Code, f.Message, HintFor(f.Code))
	}
}

func TestListedFailureIncludesHint(t *testing.T) {
	n, _ := sessionsNode(t)
	failed := ask(t, n, SendRequest{Body: "count", Session: "busy-1"})
	if failed.Hint == "" {
		t.Fatal("fixture has no failure hint")
	}

	list, _, err := n.List(ctx, me, store.Filter{State: store.Failed, Limit: 10})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v, %v", list, err)
	}
	if list[0].Hint != failed.Hint {
		t.Fatalf("list omitted hint: got %q, want %q", list[0].Hint, failed.Hint)
	}
}

type reviewSessionAgent struct {
	acp.Agent
	list func(acp.ListSessionsRequest) (acp.ListSessionsResponse, error)
	load func(acp.LoadSessionRequest) (acp.LoadSessionResponse, error)
}

func (a *reviewSessionAgent) ListSessions(
	_ context.Context, r acp.ListSessionsRequest,
) (acp.ListSessionsResponse, error) {
	return a.list(r)
}

func (a *reviewSessionAgent) LoadSession(
	_ context.Context, r acp.LoadSessionRequest,
) (acp.LoadSessionResponse, error) {
	return a.load(r)
}

func reviewSessionNode(
	t *testing.T, agent *reviewSessionAgent,
) (*Node, *acpSlot, *acpProc) {
	t.Helper()
	cfg, err := config.Parse([]byte(`
agents:
  fake:
    driver: acp
    command: [unused]
    cwd: /configured
    concurrency: 2
`), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open("memory:", StoreOptions(cfg))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	n, err := New(cfg, st, nil)
	if err != nil {
		t.Fatal(err)
	}

	clientEnd, agentEnd := net.Pipe()
	t.Cleanup(func() {
		_ = clientEnd.Close()
		_ = agentEnd.Close()
	})
	acp.NewAgentSideConnection(agent, agentEnd, agentEnd)
	p := &acpProc{
		conn: acpx.Dial(nil, clientEnd, clientEnd, acpx.WireOptions{}),
		init: acp.InitializeResponse{
			AgentCapabilities: acp.AgentCapabilities{
				LoadSession: true,
				SessionCapabilities: acp.SessionCapabilities{
					List: &acp.SessionListCapabilities{},
				},
			},
		},
		sessions: map[string]acp.SessionId{},
		done:     make(chan struct{}),
	}
	slot := n.acp["fake"].slots[0]
	slot.proc = p
	return n, slot, p
}
