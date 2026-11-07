package node

import (
	"context"
	"errors"
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
	go func() {
		defer close(t.done)
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
