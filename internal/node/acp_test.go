package node

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/acptest"
	"github.com/k1dory/acpost/internal/store"
)

func TestMain(m *testing.M) {
	if os.Getenv("ACPOST_FAKE_AGENT") == "1" {
		acptest.Run()
	}
	os.Exit(m.Run())
}

// acpNode is a node with one acp agent "fake" running the scripted agent, configured by extra
// YAML lines under the agent.
func acpNode(t testing.TB, extra string) *Node {
	t.Helper()
	state := t.TempDir()
	return newNode(t, fmt.Sprintf(`
limits: {max_reply: 256KiB, max_attempts: 2}
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
%s`, os.Args[0], state, indent(extra)))
}

func indent(s string) string {
	var b strings.Builder
	for _, l := range strings.Split(strings.TrimSpace(s), "\n") {
		if l != "" {
			b.WriteString("    " + l + "\n")
		}
	}
	return b.String()
}

func ask(t testing.TB, n *Node, r SendRequest) store.Message {
	t.Helper()
	if r.To == "" {
		r.To = "fake"
	}
	m, _, err := n.Send(ctx, me, r)
	if err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, m.ID, 60*time.Second) // CI runners under -race: a big prompt takes 20 s there
	if err != nil {
		t.Fatal(err)
	}
	if !w.State.Final() {
		t.Fatalf("%s still %s", w.ID, w.State)
	}
	return w
}

func TestACPEcho(t *testing.T) {
	n := acpNode(t, "preamble: false")
	w := ask(t, n, SendRequest{Body: "echo hello there"})
	if w.State != store.Done || w.Reply != "hello there" {
		t.Fatalf("%s %q %q", w.State, w.Reply, w.Error)
	}
}

func TestACPReplyLastAndAll(t *testing.T) {
	n := acpNode(t, "preamble: false")
	if w := ask(t, n, SendRequest{Body: "tools"}); w.Reply != "final" {
		t.Fatalf("last: %q", w.Reply)
	}
	m := ask(t, n, SendRequest{Body: "tools"})
	evs, err := n.Events(ctx, me, m.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	if !strings.Contains(strings.Join(kinds, ","), "tool_call") {
		t.Fatalf("events %v", kinds)
	}
	all := acpNode(t, "preamble: false\nreply: all")
	if w := ask(t, all, SendRequest{Body: "tools"}); w.Reply != "thinkingfinal" {
		t.Fatalf("all: %q", w.Reply)
	}
}

func TestACPPermissions(t *testing.T) {
	allow := acpNode(t, "preamble: false\npermissions: allow")
	if w := ask(t, allow, SendRequest{Body: "permission"}); w.Reply != "allowed" {
		t.Fatalf("allow: %q", w.Reply)
	}
	deny := acpNode(t, "preamble: false\npermissions: deny")
	w := ask(t, deny, SendRequest{Body: "permission"})
	if w.Reply != "denied" {
		t.Fatalf("deny: %q", w.Reply)
	}
	evs, _ := deny.Events(ctx, me, w.ID, 0, 100)
	found := false
	for _, e := range evs {
		if e.Kind == "permission" && strings.Contains(e.Data, "rm -rf build") && strings.Contains(e.Data, "reject") {
			found = true
		}
	}
	if !found {
		t.Fatalf("permission decision not recorded: %+v", evs)
	}
}

func TestACPSessionPerTask(t *testing.T) {
	n := acpNode(t, "preamble: false")
	for i, want := range []string{"1", "2", "3"} {
		if w := ask(t, n, SendRequest{Body: "count", Task: "TASK-1"}); w.Reply != want {
			t.Fatalf("prompt %d of TASK-1: %q", i, w.Reply)
		}
	}
	if w := ask(t, n, SendRequest{Body: "count", Task: "TASK-2"}); w.Reply != "1" {
		t.Fatalf("TASK-2 shares a session: %q", w.Reply)
	}
	// Without a task, each message starts its own thread, hence its own session.
	if w := ask(t, n, SendRequest{Body: "count"}); w.Reply != "1" {
		t.Fatalf("no task: %q", w.Reply)
	}
}

func TestACPSessionPerMessage(t *testing.T) {
	n := acpNode(t, "preamble: false\nsession: message")
	ask(t, n, SendRequest{Body: "count", Task: "T"})
	if w := ask(t, n, SendRequest{Body: "count", Task: "T"}); w.Reply != "1" {
		t.Fatalf("session: message reused a session: %q", w.Reply)
	}
}

// After the agent process is stopped for idleness, the next message of the same task continues the
// session through session/load in a new process.
func TestACPIdleStopThenLoad(t *testing.T) {
	n := acpNode(t, "preamble: false\nidle_timeout: 100ms")
	ask(t, n, SendRequest{Body: "count", Task: "T"})
	first := ask(t, n, SendRequest{Body: "env FAKE_PID_UNUSED", Task: "T"})
	_ = first
	time.Sleep(500 * time.Millisecond)
	if n.agentRunning("fake") {
		t.Fatal("agent process still running after idle_timeout")
	}
	if w := ask(t, n, SendRequest{Body: "count", Task: "T"}); w.Reply != "3" {
		t.Fatalf("session not continued after restart: %q", w.Reply)
	}
}

func TestACPSerialByDefault(t *testing.T) {
	n := acpNode(t, "preamble: false")
	start := time.Now()
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); ask(t, n, SendRequest{Body: "sleep 150"}) }()
	}
	wg.Wait()
	if d := time.Since(start); d < 450*time.Millisecond {
		t.Fatalf("3 turns of 150ms took %v with concurrency 1", d)
	}
}

func TestACPConcurrency(t *testing.T) {
	n := acpNode(t, "preamble: false\nconcurrency: 3")
	start := time.Now()
	var wg sync.WaitGroup
	for range 3 {
		wg.Add(1)
		go func() { defer wg.Done(); ask(t, n, SendRequest{Body: "sleep 300"}) }()
	}
	wg.Wait()
	if d := time.Since(start); d > 800*time.Millisecond {
		t.Fatalf("3 turns of 300ms took %v with concurrency 3", d)
	}
}

func TestACPCancelRunning(t *testing.T) {
	n := acpNode(t, "preamble: false")
	m, _, _ := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 20000"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		got, _ := n.Get(ctx, me, m.ID)
		if got.State == store.Running {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("never started")
		}
		time.Sleep(10 * time.Millisecond)
	}
	start := time.Now()
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	if w, _ := n.Wait(ctx, me, m.ID, time.Second); w.State != store.Cancelled {
		t.Fatal(w.State)
	}
	// The agent was told (session/cancel) and is free for the next message at once.
	if w := ask(t, n, SendRequest{Body: "echo next"}); w.Reply != "next" || time.Since(start) > 3*time.Second {
		t.Fatalf("after cancel: %q in %v", w.Reply, time.Since(start))
	}
}

func TestACPTurnTimeout(t *testing.T) {
	n := acpNode(t, "preamble: false\nturn_timeout: 200ms")
	w := ask(t, n, SendRequest{Body: "sleep 5000"})
	if w.State != store.Failed || !strings.Contains(w.Error, "timeout") {
		t.Fatalf("%s %q", w.State, w.Error)
	}
	// The agent takes the next message. Its turn has the same 200ms: on a loaded machine one may
	// run past it too, so it is asked until one does not (what is tested is that it answers again).
	var after store.Message
	for i := 0; i < 20 && after.Reply != "alive"; i++ {
		after = ask(t, n, SendRequest{Body: "echo alive"})
	}
	if after.Reply != "alive" {
		t.Fatalf("after timeout: %s %q %q", after.State, after.Reply, after.Error)
	}
}

func TestACPCrashRetriesThenFails(t *testing.T) {
	n := acpNode(t, "preamble: false")
	w := ask(t, n, SendRequest{Body: "crash"})
	if w.State != store.Failed || w.Attempts != 2 || !strings.Contains(w.Error, "exit") {
		t.Fatalf("%s attempts=%d %q", w.State, w.Attempts, w.Error)
	}
	if w := ask(t, n, SendRequest{Body: "echo restarted"}); w.Reply != "restarted" {
		t.Fatalf("after crash: %q %q", w.Reply, w.Error)
	}
}

func TestACPRefusalFails(t *testing.T) {
	n := acpNode(t, "preamble: false")
	w := ask(t, n, SendRequest{Body: "refuse"})
	if w.State != store.Failed || !strings.Contains(w.Error, "refusal") || w.Reply != "no" {
		t.Fatalf("%s %q %q", w.State, w.Error, w.Reply)
	}
}

func TestACPReplyCapped(t *testing.T) {
	n := acpNode(t, "preamble: false")
	w := ask(t, n, SendRequest{Body: "big 1000000"})
	if w.State != store.Done || len(w.Reply) > 256<<10 || !strings.HasSuffix(w.Reply, truncatedMark) {
		t.Fatalf("%s len=%d", w.State, len(w.Reply))
	}
}

func TestACPAgentEnvironment(t *testing.T) {
	n := acpNode(t, "preamble: false")
	tok := ask(t, n, SendRequest{Body: "env ACPOST_TOKEN"}).Reply
	p, ok := n.PrincipalForToken(tok)
	if !ok || p.Address() != "agent:fake" {
		t.Fatalf("agent token %q → %v %v", tok, p, ok)
	}
	if node := ask(t, n, SendRequest{Body: "env ACPOST_NODE"}).Reply; node != n.LocalURL() {
		t.Fatalf("ACPOST_NODE %q, want %q", node, n.LocalURL())
	}
}

// A message can continue an existing ACP session of the agent (one started elsewhere, say in an
// editor): the node loads it and prompts it there.
func TestACPExistingSession(t *testing.T) {
	state := t.TempDir()
	if err := os.WriteFile(state+"/external-session", []byte("5"), 0o600); err != nil {
		t.Fatal(err)
	}
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
    preamble: false
`, os.Args[0], state))
	if w := ask(t, n, SendRequest{Body: "count", Session: "external-session"}); w.State != store.Done || w.Reply != "6" {
		t.Fatalf("not continued in the existing session: %s %q %q", w.State, w.Reply, w.Error)
	}
	if w := ask(t, n, SendRequest{Body: "count", Session: "external-session"}); w.Reply != "7" {
		t.Fatalf("second message: %q", w.Reply)
	}
	// A session the agent does not have is an error, not a silent new session.
	if w := ask(t, n, SendRequest{Body: "count", Session: "no-such-session"}); w.State != store.Failed || !strings.Contains(w.Error, "no-such-session") {
		t.Fatalf("unknown session: %s %q", w.State, w.Error)
	}
}

func TestACPPreamble(t *testing.T) {
	n := acpNode(t, "")
	w := ask(t, n, SendRequest{Body: "please\nprompt", Task: "TASK-9"})
	for _, want := range []string{"user:tester", "TASK-9", w.ID, "please\nprompt"} {
		if !strings.Contains(w.Reply, want) {
			t.Fatalf("prompt lacks %q:\n%s", want, w.Reply)
		}
	}
}

// A node restarted while a turn ran puts the message back in the queue and runs it again.
func TestACPRestartRequeues(t *testing.T) {
	dir := t.TempDir()
	cfg := fmt.Sprintf(`
store: sqlite://%s/s/store.db
agents:
  fake: {driver: acp, command: [%q], env: {ACPOST_FAKE_AGENT: "1"}, preamble: false}
`, dir, os.Args[0])
	n1, st1 := fileNode(t, cfg)
	m, _, _ := n1.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 400"})
	for {
		got, _ := n1.Get(ctx, me, m.ID)
		if got.State == store.Running {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	n1.Close()
	st1.Close()
	n2, _ := fileNode(t, cfg)
	w, _ := n2.Wait(ctx, me, m.ID, 10*time.Second)
	if w.State != store.Done || w.Reply != "slept" || w.Attempts != 2 {
		t.Fatalf("%s %q attempts=%d", w.State, w.Reply, w.Attempts)
	}
}

func sessionsNode(t testing.TB) (*Node, string) {
	t.Helper()
	state := t.TempDir()
	for i, id := range []string{"older", "newer"} {
		path := state + "/" + id
		if err := os.WriteFile(path, []byte(fmt.Sprint(10*(i+1))), 0o600); err != nil {
			t.Fatal(err)
		}
		mt := time.Now().Add(time.Duration(i-2) * time.Hour)
		os.Chtimes(path, mt, mt)
	}
	return newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
    preamble: false
  box: {driver: inbox}
`, os.Args[0], state)), state
}

func TestACPSessionsList(t *testing.T) {
	n, _ := sessionsNode(t)
	list, next, err := n.Sessions(ctx, me, "fake", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != "newer" || list[1].ID != "older" || list[0].Title == "" || list[0].Cwd != "/fake" || next != "" {
		t.Fatalf("sessions %+v next=%q", list, next)
	}
	if _, _, err := n.Sessions(ctx, me, "box", "", ""); code(err) != "unsupported" || hintOf(err) == "" {
		t.Fatalf("sessions of an inbox agent: %v", err)
	}
	if _, _, err := n.Sessions(ctx, me, "ghost", "", ""); code(err) != "not_found" || !strings.Contains(hintOf(err), "acpost agents") {
		t.Fatalf("sessions of an unknown agent: %v / %q", err, hintOf(err))
	}
}

func TestACPSessionLatest(t *testing.T) {
	n, _ := sessionsNode(t)
	w := ask(t, n, SendRequest{Body: "count", Session: "latest"})
	if w.State != store.Done || w.Reply != "21" || w.Meta["acp_session"] != "newer" {
		t.Fatalf("latest: %s %q meta=%v %q", w.State, w.Reply, w.Meta, w.Error)
	}
}

// What an agent gets back when the session is open elsewhere: a code, a sentence and what to do,
// not the agent's JSON-RPC error.
func TestACPSessionBusyExplained(t *testing.T) {
	n, _ := sessionsNode(t)
	w := ask(t, n, SendRequest{Body: "count", Session: "busy-1"})
	if w.State != store.Failed || w.ErrorCode != "session_busy" || w.Hint == "" {
		t.Fatalf("%s code=%q hint=%q", w.State, w.ErrorCode, w.Hint)
	}
	for _, raw := range []string{"-32600", "{", "thread_active_writer", "Invalid request"} {
		if strings.Contains(w.Error, raw) {
			t.Fatalf("raw agent error leaks into the explanation: %q", w.Error)
		}
	}
	if w2 := ask(t, n, SendRequest{Body: "count", Session: "nope"}); w2.ErrorCode != "session_not_found" || !strings.Contains(w2.Hint, "acpost sessions") {
		t.Fatalf("unknown session: code=%q hint=%q err=%q", w2.ErrorCode, w2.Hint, w2.Error)
	}
}

func TestACPFailureCodes(t *testing.T) {
	n := acpNode(t, "preamble: false\nturn_timeout: 300ms")
	for body, want := range map[string]string{"crash": "agent_crashed", "sleep 5000": "turn_timeout", "refuse": "agent_stopped"} {
		if w := ask(t, n, SendRequest{Body: body}); w.ErrorCode != want || w.Hint == "" {
			t.Fatalf("%s: code=%q hint=%q error=%q", body, w.ErrorCode, w.Hint, w.Error)
		}
	}
	bad := newNode(t, "agents:\n  broken: {driver: acp, command: [/nonexistent/agent]}\nlimits: {max_attempts: 1}\n")
	if w := ask(t, bad, SendRequest{To: "broken", Body: "x"}); w.ErrorCode != "agent_start" || w.Hint == "" {
		t.Fatalf("missing command: code=%q hint=%q error=%q", w.ErrorCode, w.Hint, w.Error)
	}
}
