package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A chain waited for in part: task T's turn A sends B and waits for it; B sends C in task T and
// goes on. No cycle (B ends by itself, then A, then C runs): C queues and is done after A.
func TestPartlyWaitedChainQueues(t *testing.T) {
	n := roundNode(t, nil, "concurrency: 3")
	a, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 2000", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	pa := turnPrincipal(t, n, a.ID)
	b, _, err := n.Send(ctx, pa, SendRequest{To: "fake", Body: "sleep 600", Task: "U"})
	if err != nil {
		t.Fatal(err)
	}
	go n.Wait(ctx, pa, b.ID, 10*time.Second) // A waits for B
	pb := turnPrincipal(t, n, b.ID)
	c, _, err := n.Send(ctx, pb, SendRequest{To: "fake", Body: "count", Task: "T"}) // B does not wait for C
	if err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, c.ID, 20*time.Second)
	if err != nil || w.State != store.Done || w.Reply != "2" {
		t.Fatalf("C: %s %s %q reply=%q %v (want done after A, count 2)", w.State, w.ErrorCode, w.Error, w.Reply, err)
	}
}

// waitCycle runs a chain from task T's turn through hops more turns, each waiting for the next, the
// last sending a message in task T that its sender waits for: a cycle back to T's turn, refused
// at once.
func waitCycle(t *testing.T, hops int) {
	n := roundNode(t, nil, "concurrency: 4")
	a, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 6000", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	p := turnPrincipal(t, n, a.ID)
	for i := 0; i < hops; i++ {
		m, _, err := n.Send(ctx, p, SendRequest{To: "fake", Body: "sleep 6000", Task: "U" + string(rune('a'+i))})
		if err != nil {
			t.Fatal(err)
		}
		go n.Wait(ctx, p, m.ID, 10*time.Second)
		p = turnPrincipal(t, n, m.ID)
	}
	c, _, err := n.Send(ctx, p, SendRequest{To: "fake", Body: "count", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	go n.Wait(ctx, p, c.ID, 10*time.Second)
	w, err := n.Wait(ctx, me, c.ID, 3*time.Second)
	if err != nil || w.State != store.Failed || w.ErrorCode != "refused" {
		t.Fatalf("the cycle's last message: %s %s %q %v (want refused at once)", w.State, w.ErrorCode, w.Error, err)
	}
}

func TestTwoHopWaitCycleIsRefused(t *testing.T)   { waitCycle(t, 1) }
func TestThreeHopWaitCycleIsRefused(t *testing.T) { waitCycle(t, 2) }
