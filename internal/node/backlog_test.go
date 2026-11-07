package node

import (
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// Kept for later, then started: a message to its agent with its text, gone from the backlog;
// started twice, once; a refused send puts it back.
func TestBacklog(t *testing.T) {
	n := newNode(t, inboxCfg)
	if _, err := n.PutBacklogItem(ctx, Principal{Kind: "agent", Name: "bot"}, store.BacklogItem{Title: "x"}); code(err) != "refused" {
		t.Fatalf("an agent: %v", err)
	}
	b, err := n.PutBacklogItem(ctx, me, store.BacklogItem{Title: "Fix the parser", Body: "it chokes on tabs", Agent: "bot"})
	if err != nil || b.ID == "" || b.Agent != "agent:bot" {
		t.Fatalf("%+v %v", b, err)
	}
	other, _ := n.PutBacklogItem(ctx, me, store.BacklogItem{Title: "To nobody"})
	if list, _ := n.Backlog(ctx, me); len(list) != 2 {
		t.Fatalf("%+v", list)
	}
	m, err := n.StartBacklogItem(ctx, me, b.ID, "")
	if err != nil || m.To != "agent:bot" {
		t.Fatalf("%+v %v", m, err)
	}
	got, _ := n.st.Get(ctx, m.ID)
	if got.Body != "Fix the parser\n\nit chokes on tabs" {
		t.Fatalf("%q", got.Body)
	}
	if _, err := n.StartBacklogItem(ctx, me, b.ID, ""); code(err) != "not_found" {
		t.Fatalf("started twice: %v", err)
	}
	// Sent, but the node stopped before it left the backlog: started again, the same message.
	c, _ := n.PutBacklogItem(ctx, me, store.BacklogItem{Title: "Again", Agent: "bot"})
	m1, _, _ := n.Send(ctx, me, SendRequest{To: "agent:bot", Body: "Again", IdemKey: "bl:" + c.ID})
	m2, err := n.StartBacklogItem(ctx, me, c.ID, "")
	if err != nil || m2.ID != m1.ID {
		t.Fatalf("a repeat sent again: %s vs %s (%v)", m2.ID, m1.ID, err)
	}
	// No agent to give it to: it stays.
	if _, err := n.StartBacklogItem(ctx, me, other.ID, ""); code(err) != "invalid" {
		t.Fatalf("no agent: %v", err)
	}
	if _, err := n.StartBacklogItem(ctx, me, other.ID, "agent:nobody@elsewhere"); err == nil {
		t.Fatal("sent to nobody")
	}
	if list, _ := n.Backlog(ctx, me); len(list) != 1 || list[0].ID != other.ID {
		t.Fatalf("the item did not come back: %+v", list)
	}
	if err := n.DeleteBacklogItem(ctx, me, other.ID); err != nil {
		t.Fatal(err)
	}
}
