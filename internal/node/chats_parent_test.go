package node

import (
	"testing"
	"time"
)

// A thread an agent started while it handled a message of a chat is listed as a chat under that
// one; the person's own chats have no parent.
func TestChatsNameTheChatAnAgentStartedThemFrom(t *testing.T) {
	n := newNode(t, `
agents:
  lead: {driver: inbox}
  helper: {driver: inbox}
`)
	root, _, err := n.Send(ctx, me, SendRequest{To: "lead", Body: "build it"})
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := n.Claim(ctx, me, "lead", time.Second, time.Minute)
	if err != nil || !ok || got.ID != root.ID {
		t.Fatalf("%v %v %v", got.ID, ok, err)
	}
	// The lead, at work on it, asks the helper.
	tok := n.issueAgentToken("lead")
	n.setCurrent(tok, got.ID, got.LeaseID)
	lead, _ := n.PrincipalForToken(tok)
	sub, _, err := n.Send(ctx, lead, SendRequest{To: "helper", Body: "the API part"})
	if err != nil {
		t.Fatal(err)
	}
	if sub.Parent != root.ID || sub.Thread == root.Thread {
		t.Fatalf("the helper's message: parent %q thread %q", sub.Parent, sub.Thread)
	}
	chats, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	parents := map[string]string{}
	for _, c := range chats.Items {
		parents[c.Key] = c.Parent
	}
	if parents["thread:"+sub.Thread] != "thread:"+root.Thread || parents["thread:"+root.Thread] != "" {
		t.Fatalf("%v", parents)
	}
}
