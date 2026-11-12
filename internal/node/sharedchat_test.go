package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

// sharedNode is a node with two acp agents, "fake" and "other", each a scripted agent of its own.
func sharedNode(t testing.TB) *Node {
	t.Helper()
	agent := func(name string) string {
		return fmt.Sprintf("  %s:\n    driver: acp\n    command: [%q]\n    env: {ACPOST_FAKE_AGENT: \"1\", FAKE_STATE: %q}\n", name, os.Args[0], t.TempDir())
	}
	return newNode(t, "limits: {max_reply: 256KiB, max_attempts: 2}\nagents:\n"+agent("fake")+agent("other"))
}

const sharedHead = "This chat is shared with other agents"

// An agent called into a thread another one answered in is told what was asked and answered
// there, once: its next turn is told only what came since. Its own turns are never told to it.
func TestSharedChatTellsWhatOthersSaid(t *testing.T) {
	n := sharedNode(t)
	first := ask(t, n, SendRequest{To: "fake", Body: "echo one"})
	th := first.Thread

	got := ask(t, n, SendRequest{To: "other", Thread: th, Body: "prompt"}).Reply
	for _, want := range []string{sharedHead, "[user:tester asked agent:fake]\necho one\n[agent:fake answered]\none\n", "The message to you:\n\nprompt"} {
		if !strings.Contains(got, want) {
			t.Fatalf("the agent called in was not told %q:\n%s", want, got)
		}
	}
	// Nothing new since: nothing told again.
	if again := ask(t, n, SendRequest{To: "other", Thread: th, Body: "prompt"}).Reply; strings.Contains(again, sharedHead) {
		t.Fatalf("told again what it was told:\n%s", again)
	}
	// The first agent is told the other's turns, not its own (a thread of its own: no answer in it
	// quotes a prompt).
	th = ask(t, n, SendRequest{To: "fake", Body: "echo a"}).Thread
	ask(t, n, SendRequest{To: "other", Thread: th, Body: "echo b"})
	back := ask(t, n, SendRequest{To: "fake", Thread: th, Body: "prompt"}).Reply
	if !strings.Contains(back, "[user:tester asked agent:other]\necho b\n[agent:other answered]\nb\n") {
		t.Fatalf("the first agent was not told the other's turn:\n%s", back)
	}
	if strings.Contains(back, "asked agent:fake]") {
		t.Fatalf("an agent was told its own turn:\n%s", back)
	}
	// And then only what came after.
	ask(t, n, SendRequest{To: "other", Thread: th, Body: "echo late"})
	next := ask(t, n, SendRequest{To: "fake", Thread: th, Body: "prompt"}).Reply
	if !strings.Contains(next, "echo late\n[agent:other answered]\nlate\n") || strings.Count(next, "asked agent:other]") != 1 {
		t.Fatalf("the next turn is told what came since, once:\n%s", next)
	}
}

// A chat of one agent is as it was: nothing is put before its messages.
func TestSharedChatOneAgentIsToldNothing(t *testing.T) {
	n := sharedNode(t)
	first := ask(t, n, SendRequest{To: "fake", Body: "echo one"})
	if got := ask(t, n, SendRequest{To: "fake", Thread: first.Thread, Body: "prompt"}).Reply; strings.Contains(got, sharedHead) {
		t.Fatalf("a chat of one agent:\n%s", got)
	}
}

// What a token client put in a thread of the same name is not handed to an agent as the
// conversation of the node's user.
func TestSharedChatLeavesOutOtherSenders(t *testing.T) {
	n := sharedNode(t)
	first := ask(t, n, SendRequest{To: "fake", Body: "echo one"})
	ci := Principal{Kind: "client", Name: "ci", Token: &config.Token{SendTo: []string{"*"}}}
	m, _, err := n.Send(ctx, ci, SendRequest{To: "fake", Thread: first.Thread, Body: "echo planted"})
	if err != nil {
		t.Fatal(err)
	}
	if w, err := n.Wait(ctx, ci, m.ID, 60*time.Second); err != nil || w.State != store.Done {
		t.Fatalf("%v %v", w.State, err)
	}
	got := ask(t, n, SendRequest{To: "other", Thread: first.Thread, Body: "prompt"}).Reply
	if !strings.Contains(got, "echo one") || strings.Contains(got, "planted") {
		t.Fatalf("a client's message told as the chat's:\n%s", got)
	}
}

// The agent called into a chat works in the chat's directory.
func TestSharedChatSameDirectory(t *testing.T) {
	n := sharedNode(t)
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first := ask(t, n, SendRequest{To: "fake", Body: "echo one", Cwd: dir})
	got := ask(t, n, SendRequest{To: "other", Thread: first.Thread, Body: "cwd"}).Reply
	if got != dir {
		t.Fatalf("the other agent runs in %q, the chat is in %q", got, dir)
	}
	chats, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range chats.Items {
		if c.Thread != first.Thread {
			continue
		}
		if c.Agent != "agent:fake" || strings.Join(c.Agents, ",") != "agent:fake,agent:other" || c.Cwd != dir {
			t.Fatalf("the chat: agent %q, agents %v, cwd %q", c.Agent, c.Agents, c.Cwd)
		}
		return
	}
	t.Fatal("the chat is not listed")
}

// Many exchanges: the latest are told, within the size, and that some are left out is said.
func TestSharedChatBounded(t *testing.T) {
	n := sharedNode(t)
	first := ask(t, n, SendRequest{To: "fake", Body: "echo 0"})
	for i := 1; i < sharedMaxTurns+3; i++ {
		ask(t, n, SendRequest{To: "fake", Thread: first.Thread, Body: fmt.Sprintf("echo %d", i)})
	}
	got := ask(t, n, SendRequest{To: "other", Thread: first.Thread, Body: "prompt"}).Reply
	if !strings.Contains(got, "(3 earlier exchanges are left out)") || strings.Contains(got, "\necho 2\n") || !strings.Contains(got, "\necho 3\n") {
		t.Fatalf("not the latest %d:\n%s", sharedMaxTurns, got)
	}
}

// What an agent of another node answered in the thread is not told as the conversation; what an
// agent asked another itself (it may not have waited for the answer) is.
func TestSharedChatEndpoints(t *testing.T) {
	n := sharedNode(t)
	th := ask(t, n, SendRequest{To: "fake", Body: "echo one"}).Thread
	// An exchange with an agent elsewhere, as the store holds one.
	far := &store.Message{ID: store.NewID(), From: "user:tester", To: "agent:reviewer@peer", Thread: th, Body: "ask far", Origin: "hub"}
	if _, _, err := n.st.Insert(ctx, far); err != nil {
		t.Fatal(err)
	}
	if err := n.st.Settle(ctx, far.ID, store.Done, "planted by the peer", "", "", 1); err != nil {
		t.Fatal(err)
	}
	// What agent:other asked agent:fake itself.
	m, _, err := n.Send(ctx, Principal{Kind: "agent", Name: "other"}, SendRequest{To: "fake", Thread: th, Body: "echo own"})
	if err != nil {
		t.Fatal(err)
	}
	if w, err := n.Wait(ctx, me, m.ID, 60*time.Second); err != nil || w.State != store.Done {
		t.Fatalf("%v %v", w.State, err)
	}
	got := ask(t, n, SendRequest{To: "other", Thread: th, Body: "prompt"}).Reply
	if strings.Contains(got, "planted") || strings.Contains(got, "ask far") {
		t.Fatalf("another node's answer told as the chat's:\n%s", got)
	}
	if !strings.Contains(got, "[agent:other asked agent:fake]\necho own\n[agent:fake answered]\nown\n") {
		t.Fatalf("what the agent asked itself is not told:\n%s", got)
	}
}

// The mark is the session's: another session of the agent in the thread (another task's) is told
// what the first was, and neither is told twice.
func TestSharedChatMarkIsTheSessions(t *testing.T) {
	agent := func(name, extra string) string {
		return fmt.Sprintf("  %s:\n    driver: acp\n    command: [%q]\n    env: {ACPOST_FAKE_AGENT: \"1\", FAKE_STATE: %q}\n%s", name, os.Args[0], t.TempDir(), extra)
	}
	n := newNode(t, "agents:\n"+agent("fake", "")+agent("other", "    session: task\n"))
	th := ask(t, n, SendRequest{To: "fake", Body: "echo one"}).Thread
	for _, task := range []string{"a", "b"} {
		if got := ask(t, n, SendRequest{To: "other", Thread: th, Task: task, Body: "prompt"}).Reply; !strings.Contains(got, "echo one\n[agent:fake answered]\none\n") {
			t.Fatalf("task %s's session was not told:\n%s", task, got)
		}
	}
	if got := ask(t, n, SendRequest{To: "other", Thread: th, Task: "a", Body: "prompt"}).Reply; strings.Contains(got, sharedHead) {
		t.Fatalf("task a's session told twice:\n%s", got)
	}
}

// No directory chosen for the thread: the agent called in runs where the first one does (its
// configured directory), not in its own.
func TestSharedChatFirstAgentsDirectory(t *testing.T) {
	a, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	b, _ := filepath.EvalSymlinks(t.TempDir())
	agent := func(name, cwd string) string {
		return fmt.Sprintf("  %s:\n    driver: acp\n    command: [%q]\n    cwd: %q\n    env: {ACPOST_FAKE_AGENT: \"1\", FAKE_STATE: %q}\n", name, os.Args[0], cwd, t.TempDir())
	}
	n := newNode(t, "agents:\n"+agent("fake", a)+agent("other", b))
	th := ask(t, n, SendRequest{To: "fake", Body: "cwd"})
	if th.Reply != a {
		t.Fatalf("the first agent runs in %q", th.Reply)
	}
	if got := ask(t, n, SendRequest{To: "other", Thread: th.Thread, Body: "cwd"}).Reply; got != a {
		t.Fatalf("the agent called in runs in %q, the chat's first agent in %q", got, a)
	}
	// A thread of its own is in its own directory still.
	if got := ask(t, n, SendRequest{To: "other", Body: "cwd"}).Reply; got != b {
		t.Fatalf("its own thread runs in %q, not its %q", got, b)
	}
}

// A thread of a task whose session an earlier thread started in a chosen directory (session:
// task): the agent called in runs in that directory, not in the first agent's configured one.
func TestSharedChatTaskSessionsDirectory(t *testing.T) {
	work, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conf, _ := filepath.EvalSymlinks(t.TempDir())
	agent := func(name, extra string) string {
		return fmt.Sprintf("  %s:\n    driver: acp\n    command: [%q]\n    env: {ACPOST_FAKE_AGENT: \"1\", FAKE_STATE: %q}\n%s", name, os.Args[0], t.TempDir(), extra)
	}
	n := newNode(t, "agents:\n"+agent("fake", fmt.Sprintf("    session: task\n    cwd: %q\n", conf))+agent("other", ""))
	ask(t, n, SendRequest{To: "fake", Task: "x", Cwd: work, Body: "echo one"})
	second := ask(t, n, SendRequest{To: "fake", Task: "x", Body: "cwd"})
	if second.Reply != work {
		t.Fatalf("the task's session runs in %q", second.Reply)
	}
	if got := ask(t, n, SendRequest{To: "other", Thread: second.Thread, Body: "cwd"}).Reply; got != work {
		t.Fatalf("the agent called in runs in %q, the task's session in %q", got, work)
	}
}
