package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// roundNode is a node with the fake agent, extra env for it and extra YAML lines under it.
func roundNode(t *testing.T, env map[string]string, extra string) *Node {
	t.Helper()
	var e strings.Builder
	fmt.Fprintf(&e, "ACPOST_FAKE_AGENT: \"1\", FAKE_STATE: %q", t.TempDir())
	for k, v := range env {
		fmt.Fprintf(&e, ", %s: %q", k, v)
	}
	return newNode(t, fmt.Sprintf(`
limits: {max_reply: 256KiB, max_attempts: 2}
agents:
  fake:
    driver: acp
    command: [%q]
    env: {%s}
    preamble: false
%s`, os.Args[0], e.String(), indent(extra)))
}

func storedSession(t *testing.T, n *Node, key string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if sid, _ := n.st.Session(ctx, "agent:fake", key); sid != "" {
			return sid
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no session stored for " + key)
	return ""
}

func waitAll(t *testing.T, n *Node, ids ...string) []store.Message {
	t.Helper()
	var out []store.Message
	for _, id := range ids {
		w, err := n.Wait(ctx, me, id, 30*time.Second)
		if err != nil || !w.State.Final() {
			t.Fatalf("%s: %s %v", id, w.State, err)
		}
		out = append(out, w)
	}
	return out
}

// A1/X2: a task's turn, a message naming the task's session by id and the task's next message
// all take turns in the one session, with slots to spare: the fake fails a prompt into a session
// another prompt is in (in any process).
func TestTaskAndItsNamedSessionTakeTurns(t *testing.T) {
	n := roundNode(t, map[string]string{"FAKE_ONE_PROMPT": t.TempDir()}, "concurrency: 3")
	a, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 700", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	sid := storedSession(t, n, "task:T")
	b, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 50", Session: sid})
	if err != nil {
		t.Fatal(err)
	}
	c, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "count", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range waitAll(t, n, a.ID, b.ID, c.ID) {
		if w.State != store.Done {
			t.Errorf("%s: %s %s %q: two prompts ran at once in session %s", w.Body, w.State, w.ErrorCode, w.Error, sid)
		}
	}
}

// X6: a message waiting for its task's lock (the task's turn runs in another slot) keeps its
// lease: it is not reaped and run again, it runs once, after the turn before it.
func TestWaitForTheTaskKeepsTheLease(t *testing.T) {
	n := roundNode(t, nil, "concurrency: 2\nlease: 300ms")
	n.setMaintainEvery(20 * time.Millisecond)
	a, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 1200", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	storedSession(t, n, "task:T")
	b, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "count", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	got := waitAll(t, n, a.ID, b.ID)
	if got[1].State != store.Done || got[1].Reply != "2" || got[1].Attempts != 1 {
		t.Fatalf("waiting message: %s reply=%q attempts=%d %s %q", got[1].State, got[1].Reply, got[1].Attempts, got[1].ErrorCode, got[1].Error)
	}
}

// A2: an agent that keeps a session to one process (codex-acp's thread_active_writer): a task's
// turns go on in its one session whichever slot takes them, the count never starts over.
func TestTaskSessionStaysOneAcrossSlots(t *testing.T) {
	n := roundNode(t, map[string]string{"FAKE_EXCLUSIVE": "1"}, "concurrency: 2")
	if w := ask(t, n, SendRequest{Body: "count", Task: "T"}); w.Reply != "1" {
		t.Fatalf("first: %s %q %q", w.State, w.Reply, w.Error)
	}
	for i := 2; i <= 6; i++ {
		// Another task's turn takes one slot: the task's next message goes to whichever is free.
		other, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 300", Task: fmt.Sprintf("U%d", i)})
		if err != nil {
			t.Fatal(err)
		}
		m, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "count", Task: "T"})
		if err != nil {
			t.Fatal(err)
		}
		got := waitAll(t, n, other.ID, m.ID)
		if w := got[1]; w.State != store.Done || w.Reply != fmt.Sprint(i) {
			t.Fatalf("turn %d of task T: %s reply=%q %s %q (a new session would say 1)", i, w.State, w.Reply, w.ErrorCode, w.Error)
		}
	}
}

// A10: a session that could not be opened for a passing reason is retried within max_attempts,
// like a crash, not failed at once.
func TestSessionOpenFailureIsRetried(t *testing.T) {
	n := roundNode(t, map[string]string{"FAKE_FAIL_NEW_ONCE": filepath.Join(t.TempDir(), "failed")}, "")
	w := ask(t, n, SendRequest{Body: "echo hi"})
	if w.State != store.Done || w.Reply != "hi" || w.Attempts != 2 {
		t.Fatalf("%s reply=%q attempts=%d %s %q", w.State, w.Reply, w.Attempts, w.ErrorCode, w.Error)
	}
}

// A8: listing sessions asks a process of its own, stopped after: no working slot's process is
// started for it (under the slot's lock, with the request's context).
func TestSessionsLeaveTheSlotsAlone(t *testing.T) {
	n := roundNode(t, nil, "")
	if _, _, err := n.Sessions(ctx, me, "fake", "", ""); err != nil {
		t.Fatal(err)
	}
	if n.acp["fake"].running() {
		t.Fatal("Sessions started a working slot's process")
	}
}

// A9: a process's maps of sessions keep nothing of the one-off sessions (session: message) its
// turns are done with.
func TestOneOffSessionsLeaveNothingBehind(t *testing.T) {
	n := roundNode(t, nil, "session: message")
	for i := 0; i < 30; i++ {
		if w := ask(t, n, SendRequest{Body: "usage 1 2 3 4 0.01"}); w.State != store.Done {
			t.Fatalf("%s %q", w.State, w.Error)
		}
	}
	s := n.acp["fake"].slots[0]
	s.mu.Lock()
	p := s.proc
	s.mu.Unlock()
	if p == nil {
		t.Fatal("no process")
	}
	s.turn.Lock() // between turns: the maps are the slot's
	defer s.turn.Unlock()
	p.cl.mu.Lock()
	loaded := len(p.cl.loadedCost)
	p.cl.mu.Unlock()
	if len(p.sessions)+len(p.models)+len(p.states)+len(p.costBase)+len(p.fresh)+loaded != 0 {
		t.Fatalf("kept: sessions %d models %d states %d costBase %d fresh %d loadedCost %d",
			len(p.sessions), len(p.models), len(p.states), len(p.costBase), len(p.fresh), loaded)
	}
}

// X1: a client token naming the task the node's user works in gets a session of its own, not the
// user's conversation; the user's goes on where it was.
func TestClientTaskSessionIsItsOwn(t *testing.T) {
	t.Setenv("CI_TOKEN_R1", "ci-secret-token-1")
	n := newNode(t, fmt.Sprintf(`
tokens:
  - {name: ci, token_env: CI_TOKEN_R1, send_to: ["*"]}
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1"}
    preamble: false
`, os.Args[0]))
	ci := Principal{Kind: "client", Name: "ci", Token: n.cfg.Tokens[0]}
	if w := ask(t, n, SendRequest{Body: "count", Task: "T"}); w.Reply != "1" {
		t.Fatalf("user: %q", w.Reply)
	}
	m, _, err := n.Send(ctx, ci, SendRequest{To: "fake", Body: "count", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if w := waitAll(t, n, m.ID)[0]; w.Reply != "1" {
		t.Fatalf("the client's message went on in the user's session of task T: count %q", w.Reply)
	}
	if w := ask(t, n, SendRequest{Body: "count", Task: "T"}); w.Reply != "2" {
		t.Fatalf("user again: %q", w.Reply)
	}
	// A session named for another node's agent is checked here, where the sender is known.
	if _, _, err := n.Send(ctx, ci, SendRequest{To: "fake@elsewhere", Body: "x", Session: "s1"}); code(err) != "refused" {
		t.Fatalf("client --session to another node: %v", err)
	}
}

// X1: a client sending into the user's thread does not inherit the thread's directory.
func TestClientDoesNotInheritAThreadsDirectory(t *testing.T) {
	t.Setenv("CI_TOKEN_R2", "ci-secret-token-2")
	n := newNode(t, fmt.Sprintf(`
tokens:
  - {name: ci, token_env: CI_TOKEN_R2, send_to: ["*"]}
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1"}
    preamble: false
`, os.Args[0]))
	dir := t.TempDir()
	root := ask(t, n, SendRequest{Body: "cwd", Cwd: dir})
	if root.Reply != dir {
		t.Fatalf("root cwd %q", root.Reply)
	}
	ci := Principal{Kind: "client", Name: "ci", Token: n.cfg.Tokens[0]}
	m, _, err := n.Send(ctx, ci, SendRequest{To: "fake", Body: "cwd", Thread: root.ID})
	if err != nil {
		t.Fatal(err)
	}
	if m.Meta[metaCwd] != "" {
		t.Fatalf("the client's message took the user's thread directory %q", m.Meta[metaCwd])
	}
	waitAll(t, n, m.ID)
}

var _ = sync.Mutex{}
