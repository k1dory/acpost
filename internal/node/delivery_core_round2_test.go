package node

import (
	"sync"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// delivery is the local copy a delivery of message id under lease makes.
func delivery(id, lease string) *store.Message {
	return &store.Message{ID: id, From: "user:x@other", To: "agent:bot", Body: "work", Origin: originHub + lease}
}

// A delivery late in arriving (the hub's first, L1) after the one that replaced it (L2) does not
// take the copy back: the newer one's relay keeps it.
func TestLateOlderDeliveryDoesNotReplaceNewer(t *testing.T) {
	n := newNode(t, hubCfg)
	id := store.NewID()
	if why, ok := n.takeDelivery(ctx, delivery(id, "L2"), 2, 3); !ok {
		t.Fatal(why)
	}
	if _, ok := n.takeDelivery(ctx, delivery(id, "L1"), 1, 3); ok {
		t.Error("the older delivery was taken after the newer")
	}
	if got, _ := n.st.Get(ctx, id); got.Origin != originHub+"L2" {
		t.Fatalf("the copy is %q: the late delivery replaced the newer", got.Origin)
	}
	// The same delivery again (a repeat of L2) and a newer one (L3) are taken.
	if _, ok := n.takeDelivery(ctx, delivery(id, "L2"), 2, 3); !ok {
		t.Fatal("a repeat of the current delivery was refused")
	}
	if _, ok := n.takeDelivery(ctx, delivery(id, "L3"), 3, 3); !ok {
		t.Fatal("a newer delivery was refused")
	}
	if got, _ := n.st.Get(ctx, id); got.Origin != originHub+"L3" {
		t.Fatalf("the copy is %q, not the newest delivery's", got.Origin)
	}
}

// Two deliveries of one message taken at once, new or adopted, end with the newer owning the copy.
func TestConcurrentDeliveriesNewerWins(t *testing.T) {
	n := newNode(t, hubCfg)
	for i := range 20 {
		id := store.NewID()
		if i%2 == 1 { // a copy of an earlier delivery is here: both adopt
			if _, ok := n.takeDelivery(ctx, delivery(id, "L0"), 0, 0); !ok {
				t.Fatal("first delivery")
			}
		}
		var wg sync.WaitGroup
		for k, lease := range []string{"L1", "L2"} {
			wg.Add(1)
			go func() {
				defer wg.Done()
				n.takeDelivery(ctx, delivery(id, lease), k+1, 0)
			}()
		}
		wg.Wait()
		if got, _ := n.st.Get(ctx, id); got.Origin != originHub+"L2" {
			t.Fatalf("round %d: the copy is %q, not the newer delivery's", i, got.Origin)
		}
	}
}
