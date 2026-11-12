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
