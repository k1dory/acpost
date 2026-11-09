package node

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// A message's events cross the hub as they are recorded: the node running the agent hands them
// to the hub's copy (relayEvents, under the message's lease; the hub keeps how far it took them, so
// a retry or a new delivery adds nothing twice), and the sender's node copies them from the hub's
// event stream onto its own copy (followHub), or at the end from its pages (copyHubEvents).

var eventKind = regexp.MustCompile(`^[a-z_]{1,32}$`)

// Events cross the hub in pages of hubEventPage: 16 KiB each at most, which JSON may escape six
// times over (control characters, <, >, &), still under any node's request and response limits
// (a response of a node with the smallest limits takes about 1 MiB). relayPages of them go at one
// wake: the relay renews its lease in between.
const (
	hubEventPage = 8
	relayPages   = 4
)

// RelayEvents takes on this node's copy the events a joined node's agent recorded for a message
// the node holds under lease, after the node's event number after; it returns how many of them
// this copy has taken (the caller goes on from there).
func (n *Node) RelayEvents(ctx context.Context, p Principal, id, lease string, after int64, evs []store.RelayedEvent) (int64, error) {
	if p.Kind != "node" {
		return 0, errf("refused", "only a joined node relays events")
	}
	m, err := n.st.Get(ctx, id)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		return 0, err // a request cut short is not a message gone
	}
	if err != nil || !n.canServe(p, m.To) {
		return 0, errf("not_found", "no message %s", id)
	}
	if len(evs) > store.MaxPage || after < 0 {
		return 0, errf("invalid", "%d events after %d: at most %d at once, after 0 or more", len(evs), after, store.MaxPage)
	}
	for _, e := range evs {
		if !eventKind.MatchString(e.Kind) {
			return 0, errf("invalid", "event kind %q: lower case letters and _", e.Kind)
		}
		// A node counts only its own agent's turns: the one this message went to.
		if e.Kind == "usage" {
			var u store.Usage
			if json.Unmarshal([]byte(e.Data), &u) != nil || u.Agent != m.To {
				return 0, errf("invalid", "usage of %q in a message to %s", u.Agent, m.To)
			}
		}
	}
	relayed, err := n.st.RelayEvents(ctx, id, lease, after, evs)
	if errors.Is(err, store.ErrLeaseLost) {
		return 0, errf("conflict", "%v", err)
	}
	if err == nil {
		n.eventsAdded()
	}
	return relayed, err
}

// relayEvents hands the hub the events of a delivered message recorded after the one numbered
// sent, up to relayPages pages; it returns how far the hub has them, whether to go on handing
// them (not after a refusal: the lease is gone, the relay learns that itself) and whether more
// are waiting.
func (n *Node) relayEvents(id, lease string, sent int64) (int64, bool, bool) {
	for range relayPages {
		page, err := n.st.Events(n.ctx, id, sent, hubEventPage)
		if err != nil || len(page) == 0 {
			return sent, true, false
		}
		evs := make([]store.RelayedEvent, len(page))
		for i, e := range page {
			evs[i] = store.RelayedEvent{Kind: e.Kind, Data: e.Data}
		}
		ctx, done := context.WithTimeout(n.ctx, 10*time.Second)
		got, err := n.hub.RelayEvents(ctx, id, lease, sent, evs)
		done()
		if err != nil {
			return sent, !isFinalErr(err), false // passing: again at the next wake
		}
		sent = got // the hub's count: past what it had already, or back to a gap's start
	}
	return sent, true, true
}

// relayAll hands the hub what is left of a final message's events before its reply (at most the
// message's event limit), renewing the lease (last renewed at extended) as it goes; after five
// failures in a row it gives up: the reply is not held back for ever by events.
func (n *Node) relayAll(id, lease string, sent int64, extended time.Time) {
	for failed := 0; failed < 5 && n.ctx.Err() == nil; {
		before := sent
		next, ok, more := n.relayEvents(id, lease, sent)
		if !ok {
			return
		}
		sent = next
		if !more {
			if page, err := n.st.Events(n.ctx, id, sent, 1); err != nil || len(page) == 0 {
				return // all handed
			}
		}
		if sent > before {
			failed = 0
		} else {
			failed++
			select { // the hub did not take them: once more shortly
			case <-n.ctx.Done():
				return
			case <-time.After(time.Second):
			}
		}
		if time.Since(extended) >= hubRenew {
			extended = time.Now()
			ctx, done := context.WithTimeout(n.ctx, 10*time.Second)
			err := n.hub.Extend(ctx, id, lease, hubLease)
			done()
			if err != nil && isFinalErr(err) {
				return
			}
		}
	}
}

// followHub copies the hub's events of a forwarded message onto its local copy as they come,
// until the message is final there; it returns the final message, or an error when the hub's
// stream is not to be had (the caller then waits for the outcome alone).
func (n *Node) followHub(id string) (store.Message, error) {
	after, err := n.st.EventCount(n.ctx, id) // the events copied before, one for one
	if err != nil {
		return store.Message{}, err
	}
	started := false
	for {
		// A stream that stays silent past hubStream may be a connection gone dead: opened again,
		// from the events copied so far.
		sctx, done := context.WithTimeout(n.ctx, hubStream)
		m, err := n.hub.Follow(sctx, id, int64(after), func(e store.Event) error {
			if !started { // the first event: the agent has the message, the local copy says so
				started = true
				gctx, gdone := context.WithTimeout(n.ctx, hubCallTimeout)
				m, err := n.hub.Get(gctx, id)
				gdone()
				if err == nil && !m.State.Final() {
					n.settleFrom(id, m, nil)
				}
			}
			after = int(e.Seq)
			return n.copyEvent(id, e)
		})
		stale := sctx.Err() != nil && n.ctx.Err() == nil
		done()
		if !stale {
			return m, err
		}
	}
}

// copyHubEvents copies, page by page, the hub's events of a forwarded message its local copy does
// not have yet: for a message followed without the stream (a restart, more forwards than long
// polls), before its outcome is settled. It says whether it got through: if not, the outcome waits
// for the next try. The hub's numbers lead (this node may keep fewer events than the hub sends).
func (n *Node) copyHubEvents(id string) bool {
	count, err := n.st.EventCount(n.ctx, id)
	if err != nil {
		return true // the local copy is gone: nothing to settle either
	}
	for after := int64(count); n.ctx.Err() == nil; {
		ctx, done := context.WithTimeout(n.ctx, 10*time.Second)
		page, err := n.hub.Events(ctx, id, after, hubEventPage)
		done()
		if err != nil {
			var ce *client.Error
			return errors.As(err, &ce) && ce.Code == "not_found" // gone there: the outcome says so
		}
		for _, e := range page {
			if n.copyEvent(id, e) != nil {
				return true
			}
			after = e.Seq
		}
		if len(page) < hubEventPage {
			return true
		}
	}
	return false
}

func (n *Node) copyEvent(id string, e store.Event) error {
	err := n.addEvent(n.ctx, id, e.Kind, e.Data, false)
	if errors.Is(err, store.ErrNotFound) {
		return err // the local copy is gone
	}
	return nil
}
