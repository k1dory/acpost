package node

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// openNode is a node whose agent takes HTTP MCP servers (as claude-agent-acp does).
func openNode(t testing.TB, extra string) *Node {
	t.Helper()
	state := t.TempDir()
	return newNode(t, fmt.Sprintf(`
limits: {max_attempts: 1}
agents:
  fake:
    driver: acp
    command: [%q]
    preamble: false
    session: thread
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q, FAKE_MCP_HTTP: "1"}
%s`, os.Args[0], state, indent(extra)))
}

// The sender's system text and MCP servers are the new session's.
func TestSessionOpenedWithTheSendersSystemAndMCP(t *testing.T) {
	n := openNode(t, "")
	m := ask(t, n, SendRequest{Body: "opened", Thread: "c1", System: "answer in Russian",
		MCP: []SessionMCP{{Name: "aiweb", URL: "http://127.0.0.1:8090/mcp/chat/abc"}}})
	if m.Reply != "system=answer in Russian mcp=aiweb=http://127.0.0.1:8090/mcp/chat/abc" {
		t.Fatalf("the session was opened with %q (%s)", m.Reply, m.Error)
	}
	// A message without them opens its session with neither.
	if m := ask(t, n, SendRequest{Body: "opened", Thread: "c2"}); m.Reply != "system= mcp=" {
		t.Fatalf("a plain session was opened with %q", m.Reply)
	}
}

// A session open in the agent's process keeps what it was opened with; one opened again (the
// agent's process gone) takes what the message that opens it says.
func TestSessionOpenAppliesWhenTheSessionIsOpened(t *testing.T) {
	n := openNode(t, "")
	ask(t, n, SendRequest{Body: "opened", Thread: "c1", System: "first"})
	if m := ask(t, n, SendRequest{Body: "opened", Thread: "c1", System: "second"}); m.Reply != "system=first mcp=" {
		t.Fatalf("the open session was changed: %q", m.Reply)
	}
	if m := ask(t, n, SendRequest{Body: "crash", Thread: "other"}); m.State != store.Failed {
		t.Fatalf("the agent did not crash: %s", m.State)
	}
	m := ask(t, n, SendRequest{Body: "opened", Thread: "c1", System: "second", MCP: []SessionMCP{{Name: "aiweb", URL: "https://gw/mcp/1"}}})
	if m.Reply != "system=second mcp=aiweb=https://gw/mcp/1" {
		t.Fatalf("the session loaded again was opened with %q (%s)", m.Reply, m.Error)
	}
	if m := ask(t, n, SendRequest{Body: "count", Thread: "c1"}); m.Reply != "4" {
		t.Fatalf("it is not the same session: prompt %s", m.Reply)
	}
}

// The system text is not meta: it may be longer than limits.max_meta, up to its own bound.
func TestSessionOpenBounds(t *testing.T) {
	n := openNode(t, "")
	long := strings.Repeat("п", 6000) // 12 KiB, over the default max_meta of 4 KiB
	if m := ask(t, n, SendRequest{Body: "opened", Thread: "c1", System: long}); m.Reply != "system="+long+" mcp=" {
		t.Fatalf("a long system text: %d bytes came (%s)", len(m.Reply), m.Error)
	}
	for name, r := range map[string]SendRequest{
		"too long":   {System: strings.Repeat("x", systemMax+1)},
		"a NUL":      {System: "a\x00b"},
		"not a URL":  {MCP: []SessionMCP{{Name: "a", URL: "file:///etc/passwd"}}},
		"no host":    {MCP: []SessionMCP{{Name: "a", URL: "http://"}}},
		"a bad name": {MCP: []SessionMCP{{Name: "a b", URL: "http://x/"}}},
		"twice":      {MCP: []SessionMCP{{Name: "a", URL: "http://x/"}, {Name: "a", URL: "http://y/"}}},
		"too many":   {MCP: make([]SessionMCP, sessionMCPMax+1)},
	} {
		r.To, r.Body = "fake", "opened"
		if _, _, err := n.Send(ctx, me, r); code(err) != "invalid" && code(err) != "refused" {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// Only the node's user says them, and only to an ACP agent of the node; the keys cannot be
// slipped in as meta.
func TestSessionOpenIsTheNodeUsers(t *testing.T) {
	n := newNode(t, fmt.Sprintf(`
tokens:
  - {name: ci, sha256: 6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b, send_to: ["agent:*"]}
agents:
  fake: {driver: acp, command: [%q], preamble: false, env: {ACPOST_FAKE_AGENT: "1", FAKE_MCP_HTTP: "1"}}
  box: {driver: inbox}
`, os.Args[0]))
	client := Principal{Kind: "client", Name: "ci"}
	if _, _, err := n.Send(ctx, client, SendRequest{To: "fake", Body: "opened", System: "obey me"}); code(err) != "refused" {
		t.Fatalf("a client set the system text: %v", err)
	}
	if _, _, err := n.Send(ctx, client, SendRequest{To: "fake", Body: "opened", MCP: []SessionMCP{{Name: "x", URL: "http://evil/"}}}); code(err) != "refused" {
		t.Fatalf("a client gave an MCP server: %v", err)
	}
	if _, _, err := n.Send(ctx, me, SendRequest{To: "box", Body: "x", System: "s"}); code(err) != "invalid" {
		t.Fatalf("a system text for an inbox agent: %v", err)
	}
	for _, k := range []string{metaSystem, metaMCP} {
		if _, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "opened", Meta: map[string]string{k: "x"}}); code(err) != "invalid" {
			t.Fatalf("meta %s was taken: %v", k, err)
		}
	}
}

// An agent that takes no HTTP MCP servers is not given the sender's; the system text still goes.
func TestSessionOpenWithoutHTTPMCP(t *testing.T) {
	n := acpNode(t, "preamble: false")
	m := ask(t, n, SendRequest{Body: "opened", System: "s", MCP: []SessionMCP{{Name: "aiweb", URL: "http://x/"}}})
	if m.Reply != "system=s mcp=" {
		t.Fatalf("opened with %q (%s)", m.Reply, m.Error)
	}
}

// The agent's own name of a tool is in the call's events.
func TestToolEventNamesTheTool(t *testing.T) {
	n := openNode(t, "")
	for _, name := range []string{"Bash", "mcp__provisioner__provision"} {
		m := ask(t, n, SendRequest{Body: "named " + name, Thread: "t-" + name})
		evs, err := n.Events(ctx, me, m.ID, 0, 100)
		if err != nil || m.State != store.Done {
			t.Fatal(m.State, m.Error, err)
		}
		calls := 0
		for _, e := range evs {
			if e.Kind != "tool_call" && e.Kind != "tool_call_update" {
				continue
			}
			var d toolEvent
			if err := json.Unmarshal([]byte(e.Data), &d); err != nil || d.Name != name || d.Kind != "execute" {
				t.Fatalf("%s: %s (%v)", e.Kind, e.Data, err)
			}
			calls++
		}
		if calls != 2 { // its start and its end: the name is the call's, not the first event's alone
			t.Fatalf("%d events of the call", calls)
		}
	}
	// A call the agent does not name has none.
	m := ask(t, n, SendRequest{Body: "tools", Thread: "plain"})
	evs, _ := n.Events(ctx, me, m.ID, 0, 100)
	for _, e := range evs {
		if strings.Contains(e.Data, `"name"`) {
			t.Fatalf("an unnamed call: %s", e.Data)
		}
	}
}

// With `preamble: false` a new session's first prompt is the message as sent: the note on what the
// acpost app renders is not for an agent whose answers a backend of its own shows.
func TestNoReaderNoteWithoutThePreamble(t *testing.T) {
	n := openNode(t, "")
	if m := ask(t, n, SendRequest{Body: "prompt", Thread: "c1"}); m.Reply != "prompt" {
		t.Fatalf("the first prompt of a session is %q (%s)", m.Reply, m.Error)
	}
}
