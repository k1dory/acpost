package node

import (
	"sync"
	"time"
)

// maxResolvers bounds the forwards whose fate is asked of the hub at once. A hub away for long
// leaves many copies forwarding (limits.max_queued_per_agent of them per address): they wait their
// turn here, each with its own pause growing to a minute, instead of each holding a goroutine of
// its own that asks the hub on its own schedule.
const maxResolvers = 8

// resolverPool runs resolveOnce for the forwards handed to it (n.resolving owns them meanwhile):
// one dispatcher, at most maxResolvers tries at a time.
type resolverPool struct {
	mu      sync.Mutex
	due     map[string]*resolveTry
	running int  // tries running now
	started bool // the dispatcher is running
	wake    chan struct{}
}

type resolveTry struct {
	next   time.Time
	pause  time.Duration
	trying bool
}

// add hands the pool a forward the caller owns (n.resolving holds it); its first try is after
// hubRetry. false: the node is closing, nothing will try it.
func (rp *resolverPool) add(n *Node, id string) bool {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	if rp.due == nil {
		rp.due, rp.wake = map[string]*resolveTry{}, make(chan struct{}, 1)
	}
	if _, ok := rp.due[id]; !ok {
		rp.due[id] = &resolveTry{next: time.Now().Add(hubRetry), pause: hubRetry}
	}
	if !rp.started {
		if !n.goWork(func() { rp.dispatch(n) }) {
			delete(rp.due, id)
			return false
		}
		rp.started = true
	}
	rp.signal()
	return true
}

func (rp *resolverPool) signal() {
	select {
	case rp.wake <- struct{}{}:
	default:
	}
}

// size is how many forwards wait in the pool, and how many tries run.
func (rp *resolverPool) size() (waiting, running int) {
	rp.mu.Lock()
	defer rp.mu.Unlock()
	return len(rp.due), rp.running
}

// dispatch starts the tries that are due, up to maxResolvers at once, until the node stops.
func (rp *resolverPool) dispatch(n *Node) {
	for {
		rp.mu.Lock()
		now, wait := time.Now(), time.Minute
		for id, t := range rp.due {
			if t.trying {
				continue
			}
			if d := t.next.Sub(now); d > 0 {
				wait = min(wait, d)
				continue
			}
			if rp.running >= maxResolvers {
				break // a try ending wakes the dispatcher
			}
			t.trying = true
			rp.running++
			n.goLoop(func() { rp.try(n, id) })
		}
		rp.mu.Unlock()
		select {
		case <-n.ctx.Done():
			return
		case <-rp.wake:
		case <-time.After(wait):
		}
	}
}

// try is one resolveOnce: a forward settled leaves the pool (and n.resolving); one not is tried
// again after its pause, doubled.
func (rp *resolverPool) try(n *Node, id string) {
	settled := n.ctx.Err() == nil && n.resolveOnce(id)
	rp.mu.Lock()
	rp.running--
	if t := rp.due[id]; t != nil {
		if settled {
			delete(rp.due, id)
			n.resolving.Delete(id)
		} else {
			t.trying, t.next, t.pause = false, time.Now().Add(t.pause), min(2*t.pause, time.Minute)
		}
	}
	rp.mu.Unlock()
	rp.signal()
}
