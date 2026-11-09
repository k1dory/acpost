package node

import (
	"sync"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// A start whose answer was lost is repeated: the same message, not a 404.
func TestBacklogStartRepeatedReturnsSameMessage(t *testing.T) {
	n := newNode(t, inboxCfg)
	b, err := n.PutBacklogItem(ctx, me, store.BacklogItem{Title: "Fix it", Agent: "bot"})
	if err != nil {
		t.Fatal(err)
	}
	m, err := n.StartBacklogItem(ctx, me, b.ID, "")
	if err != nil {
		t.Fatal(err)
	}
	again, err := n.StartBacklogItem(ctx, me, b.ID, "")
	if err != nil || again.ID != m.ID {
		t.Fatalf("repeat: %s %v, first %s", again.ID, err, m.ID)
	}
	if _, err := n.StartBacklogItem(ctx, me, "bl_none", ""); code(err) != "not_found" {
		t.Fatalf("an item never there: %v", err)
	}
}

// An edit saved while the item is started is not lost: it is the message sent, or it stays on
// the backlog.
func TestBacklogEditDuringStartNotLost(t *testing.T) {
	n := newNode(t, "limits: {max_queued_per_agent: 1000}\nagents:\n  bot: {driver: inbox}\n")
	for i := range 200 {
		b, err := n.PutBacklogItem(ctx, me, store.BacklogItem{Title: "v1", Agent: "bot"})
		if err != nil {
			t.Fatal(err)
		}
		var wg sync.WaitGroup
		var m store.Message
		var serr error
		wg.Add(2)
		go func() { defer wg.Done(); m, serr = n.StartBacklogItem(ctx, me, b.ID, "") }()
		go func() {
			defer wg.Done()
			v2 := b
			v2.Title = "v2"
			_, _ = n.PutBacklogItem(ctx, me, v2)
		}()
		wg.Wait()
		if serr != nil {
			t.Fatal(serr)
		}
		got, _ := n.st.Get(ctx, m.ID)
		left, lerr := n.st.BacklogItemOf(ctx, b.ID)
		if got.Body != "v2" && (lerr != nil || left.Title != "v2") {
			t.Fatalf("round %d: v1 sent and the edit v2 is gone", i)
		}
		_ = n.st.DeleteBacklogItem(ctx, b.ID)
	}
}
