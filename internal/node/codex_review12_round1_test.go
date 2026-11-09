package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// turnPrincipal is the principal of the agent process whose turn runs message id.
func turnPrincipal(t *testing.T, n *Node, id string) Principal {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		for _, s := range n.acp["fake"].slots {
			if p, ok := n.PrincipalForToken(s.token); ok && n.currentOf(p) == id {
				return p
			}
		}
	}
	t.Fatalf("the turn of %s never started", id)
	return Principal{}
}

// A chain without a wait: task T's turn sends a message and goes on (no --wait); that message's
// turn sends one back with task T. Nobody waits for anybody: the reply queues behind T's turn and
// runs after it, it is not refused.
func TestChainWithoutAWaitQueues(t *testing.T) {
	n := roundNode(t, nil, "concurrency: 2")
	a, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 2000", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	pa := turnPrincipal(t, n, a.ID)
	b, _, err := n.Send(ctx, pa, SendRequest{To: "fake", Body: "sleep 600", Task: "U"}) // sent, not waited for
	if err != nil {
		t.Fatal(err)
	}
	pb := turnPrincipal(t, n, b.ID)
	c, _, err := n.Send(ctx, pb, SendRequest{To: "fake", Body: "count", Task: "T"}) // the answer, in task T
	if err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, c.ID, 20*time.Second)
	if err != nil || w.State != store.Done || w.Reply != "2" {
		t.Fatalf("the answer in task T: %s %s %q reply=%q %v (want done after T's turn, count 2)", w.State, w.ErrorCode, w.Error, w.Reply, err)
	}
}
