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
	if again, err := n.StartBacklogItem(ctx, me, b.ID, ""); err != nil || again.ID != m.ID {
		t.Fatalf("started twice: %s %v (the first: %s)", again.ID, err, m.ID)
	}
	// Sent, but the node stopped before it left the backlog: started again, the same message.
	c, _ := n.PutBacklogItem(ctx, me, store.BacklogItem{Title: "Again", Agent: "bot"})
	c2, _ := n.PutBacklogItem(ctx, me, store.BacklogItem{Title: "Again too", Agent: "bot"})
	n.st.DeleteBacklogItem(ctx, c.ID) // (c only gives the key's form)
	m1, err := n.StartBacklogItem(ctx, me, c2.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	n.st.PutBacklogItem(ctx, c2) // as if taking it off had failed
	m2, err := n.StartBacklogItem(ctx, me, c2.ID, "")
	if err != nil || m2.ID != m1.ID {
		t.Fatalf("a repeat sent again: %s vs %s (%v)", m2.ID, m1.ID, err)
	}
	// Edited since: a new message with the new text.
	c2.Title = "Again, changed"
	n.st.PutBacklogItem(ctx, c2)
	m3, err := n.StartBacklogItem(ctx, me, c2.ID, "")
	if err != nil || m3.ID == m1.ID {
		t.Fatalf("the edit was lost: %s %v", m3.ID, err)
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
