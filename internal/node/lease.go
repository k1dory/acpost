package node

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// turnLease watches a message while its turn runs: its cancel ends the turn's context
// (cancelled), its lease is extended a third of the way through, and a lease lost (the node
// stalled past it: another attempt owns the message now) ends the turn too (lost).
type turnLease struct {
	ctx       context.Context
	cancel    context.CancelFunc
	cancelled atomic.Bool
	lost      atomic.Bool
	done      chan struct{}
}

func (n *Node) watchTurn(m store.Message, lease time.Duration) *turnLease {
	ctx, cancel := context.WithCancel(n.ctx)
	t := &turnLease{ctx: ctx, cancel: cancel, done: make(chan struct{})}
	n.held.add(m.ID, m.LeaseID, lease)
	go func() {
		defer close(t.done)
		defer n.held.remove(m.ID, m.LeaseID)
		next := time.Now().Add(lease / 3)
		for {
			ch := n.changes()
			select {
			case <-ctx.Done():
				return
			case <-ch:
			case <-time.After(time.Until(next)):
			}
			if cur, err := n.st.Get(ctx, m.ID); err == nil && cur.State == store.Cancelled {
				t.cancelled.Store(true)
				cancel()
				return
			}
			if time.Now().After(next) {
				if err := n.st.Extend(ctx, m.ID, m.LeaseID, lease); errors.Is(err, store.ErrLeaseLost) {
					t.lost.Store(true)
					cancel()
					return
				}
				next = time.Now().Add(lease / 3)
			}
		}
	}()
	return t
}

// stop ends the watch (and the turn's context).
func (t *turnLease) stop() { t.cancel(); <-t.done }

// heldLeases are the leases of the turns this process runs. The maintenance loop renews them just
// before it reaps leases run out: after the process stood still past a lease (a laptop asleep, a
// long pause) the reaper would otherwise requeue a turn still running here, and the message would
// run twice on the node's own agent. A lease of a process gone (an inbox consumer) is not here.
type heldLeases struct {
	mu sync.Mutex
	m  map[string]heldLease // by message id
}

type heldLease struct {
	lease string
	d     time.Duration
}

func (h *heldLeases) add(id, lease string, d time.Duration) {
	h.mu.Lock()
	if h.m == nil {
		h.m = map[string]heldLease{}
	}
	h.m[id] = heldLease{lease, d}
	h.mu.Unlock()
}

func (h *heldLeases) remove(id, lease string) {
	h.mu.Lock()
	if h.m[id].lease == lease {
		delete(h.m, id)
	}
	h.mu.Unlock()
}

// renew extends every held lease by its own length (one lost meanwhile stays lost: the turn's
// watch learns it).
func (h *heldLeases) renew(ctx context.Context, st *store.Store) {
	h.mu.Lock()
	held := make(map[string]heldLease, len(h.m))
	for id, l := range h.m {
		held[id] = l
	}
	h.mu.Unlock()
	for id, l := range held {
		_ = st.Extend(ctx, id, l.lease, l.d)
	}
}
