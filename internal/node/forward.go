package node

import (
	"context"
	"errors"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// Forwarding: a message to an agent on another node goes to the hub, and the node keeps a local
// copy. The copy is stored before the hub is asked (origin forwarding), under the sender's key, so
// a request that ends after the hub took the message still leaves it. Then:
//
//	forwarding  the hub's answer is not known yet: the resolvers learn it from the hub (resolveOnce)
//	remote      the hub has it: the copy follows the hub's (followSweep, follow)
//	unsent      not sent for certain (refused, or the hub never got it); its key is free again
//
// A cancel asked for here while a copy is still forwarding is recorded (cancel_wanted), not done:
// the copy stays unfinished until the hub's verdict, so no one waiting sees an outcome that the
// hub then contradicts. The hub cancels it, or had ended it before: either way the copy takes the
// hub's outcome. The hub never got it: the copy ends cancelled.
//
// One goroutine at a time owns a forwarding copy (n.resolving): the send while its request is out,
// then the resolver it hands the copy to. Nothing settles a copy while its send may still reach
// the hub, and the hub's "I have no such message" counts only once hubAbsence has passed since
// the copy was stored (a send may arrive late).

const (
	originForwarding = "forwarding"
	originUnsent     = "unsent"
)

// Store writes of the forwarding path (a test makes one fail).
var (
	finishForward  = (*store.Store).FinishForward
	forwardNotSent = (*store.Store).ForwardNotSent
	replaceForward = (*store.Store).ReplaceForward
	// afterCancelRead runs in Cancel between reading the message and cancelling it (a test
	// finishes a forward there).
	afterCancelRead = func(id string) {}
)

// pendingErr is what a send with the key of a copy still forwarding gets: not a success (the hub
// may not have it), but a cause to try again.
func pendingErr(id string) error {
	return errh("unavailable", "send again shortly with the same key: the hub's answer is being learned",
		"%s is on its way to the hub, not confirmed yet", id)
}

// forward sends a message to the hub and keeps a local copy that follows it.
func (n *Node) forward(ctx context.Context, m *store.Message, to Address) (store.Message, bool, error) {
	req := client.SendRequest{To: to.String(), Body: m.Body, Task: m.Task, Thread: m.Thread, Meta: m.Meta, Files: m.Files,
		ID: m.ID, From: n.qualify(m.From), Parent: m.Parent, Depth: m.Depth}
	if !m.ExpiresAt.IsZero() {
		req.TTL = time.Until(m.ExpiresAt).Round(time.Millisecond).String()
	}
	lctx := context.WithoutCancel(ctx) // the local copy's writes outlive the request
	pending := *m
	pending.To, pending.Origin = to.String(), originForwarding
	n.resolving.Store(m.ID, true) // this send owns the copy until it hands it on
	owned := true
	defer func() {
		if owned {
			n.resolving.Delete(m.ID)
		}
	}()
	handOn := func() { owned = false; n.resolveOwned(m.ID) }
	// Copies the hub has not answered for count against the limit of the address they go to.
	lim := n.cfg.Limits.MaxQueuedPerAgent
	got, created, err := n.st.InsertLimited(lctx, &pending, lim)
	if errors.Is(err, store.ErrQueueFull) {
		return store.Message{}, false, errh("unavailable", "send again once the hub answers",
			"%s has %d messages waiting for the hub's answer (limits.max_queued_per_agent)", pending.To, lim)
	}
	if err != nil {
		return store.Message{}, false, n.fileErr(err, m.ID)
	}
	if !created { // a repeat of the key, sent at the same time
		if got.Origin == originForwarding || got.Origin == originUnsent { // (unsent: released just now)
			return store.Message{}, false, pendingErr(got.ID)
		}
		return hideLease(got), false, nil
	}
	sent, created, err := n.hub.Send(ctx, req, m.IdemKey)
	if err != nil {
		if !hubRefused(err) || !n.notSent(lctx, m.ID, hubErr(err)) {
			handOn() // the hub may have it, or the write failed: learned from the hub
		}
		return store.Message{}, false, hubErr(err)
	}
	if !created && sent.ID != m.ID {
		copied, err := n.takeHubRepeat(ctx, m.ID, sent)
		if err != nil {
			handOn()
			return store.Message{}, false, err
		}
		return hideLease(copied), false, nil
	}
	if ok, err := finishForward(n.st, lctx, m.ID, originForwarding, originRemote, sent); err != nil || !ok {
		// The hub has it, the copy is not marked so (a write failed, or it was cancelled
		// meanwhile): the resolver finishes it, carrying a cancel to the hub.
		handOn()
	} else {
		n.startFollow(m.ID)
	}
	if cur, err := n.st.Get(lctx, m.ID); err == nil {
		got = cur
	}
	n.notify()
	return hideLease(got), created, nil
}

// hubRefused says the hub answered a send with a refusal: the message is not there.
func hubRefused(err error) bool {
	var ce *client.Error
	return errors.As(err, &ce) && ce.Code != "internal" && ce.Code != "unavailable"
}

// takeHubRepeat replaces the copy stored for a send (id) with a copy of the message the hub kept
// for the same key before (sent): its first send, whose local copy is gone or was never made. The
// replacement is one store step, a cancel done here meanwhile carried over.
//
// Files this node refuses for good (over its limits) are left out: the copy still takes the key and
// the cancel, and follows the hub's outcome.
func (n *Node) takeHubRepeat(ctx context.Context, id string, sent store.Message) (store.Message, error) {
	// The hub's message is known: a cancel asked for goes to it first, so nothing below (a
	// download, the store's room, a write) holds it back. The copy is replaced at this try or a
	// later one.
	n.cancelThrough(id, sent.ID)
	if listed := pick(sent.Files, false); len(listed) > 0 {
		fctx, done := context.WithTimeout(ctx, hubFilesTimeout)
		got, err := n.fetchFiles(fctx, sent.ID, false, listed, "the message")
		done()
		var e *Error
		switch {
		case err == nil:
			sent.Files = got
		case errors.As(err, &e) || filesRefused(err):
			n.log.Warn("the files of a message the hub kept are refused here; its copy goes without them",
				"id", sent.ID, "err", err)
			sent.Files = nil
		default:
			return store.Message{}, hubErr(err)
		}
	} else {
		sent.Files = nil
	}
	got, err := replaceForward(n.st, context.WithoutCancel(ctx), id, originForwarding, originRemote, sent)
	if err != nil {
		return store.Message{}, err
	}
	n.notify()
	if got.Origin == originForwarding {
		n.resolveLater(got.ID) // cancelled here: the cancel goes to the hub's message
	} else {
		n.startFollow(got.ID)
	}
	return got, nil
}

// notSent marks a forwarded message as not sent for certain; false: the write failed.
func (n *Node) notSent(ctx context.Context, id string, why error) bool {
	ok, err := forwardNotSent(n.st, ctx, id, originForwarding, originUnsent, "not sent through the hub: "+why.Error())
	if err != nil {
		return false
	}
	if ok {
		n.notify()
	}
	return true
}

// resolveLater hands a message to the resolvers, unless something owns it already (its send,
// or a resolver).
func (n *Node) resolveLater(id string) {
	if _, busy := n.resolving.LoadOrStore(id, true); busy {
		return
	}
	n.resolveOwned(id)
}

// resolveOwned hands a message the caller owns (n.resolving holds it) to the resolvers, which let
// it go when it is settled. They learn from the hub what became of a message stored to be sent
// through it (the hub did not answer, the request ended, the node stopped, the copy was cancelled
// meanwhile), trying again with a growing pause until they know, a bounded number at once
// (resolverPool). They do not send the message again: its sender was told the send failed.
func (n *Node) resolveOwned(id string) {
	if !n.resolvers.add(n, id) {
		n.resolving.Delete(id)
	}
}

// resolveOnce is one try of the resolvers; it reports whether the message is settled.
func (n *Node) resolveOnce(id string) bool {
	local, err := n.st.Get(n.ctx, id)
	if errors.Is(err, store.ErrNotFound) {
		return true
	}
	if err != nil {
		return false
	}
	if local.Origin != originForwarding {
		return true
	}
	ctx, done := context.WithTimeout(n.ctx, hubCallTimeout)
	hm, err := n.hub.Get(ctx, id)
	done()
	var ce *client.Error
	switch {
	case err == nil:
		return n.foundOnHub(id, local, hm)
	case errors.As(err, &ce) && ce.Code == "not_found":
	default:
		return false // the hub did not say: again later
	}
	// Not on the hub under this id. A send with a key may have been kept under the id of an
	// earlier send of the key.
	key, err := n.st.IdemKeyOf(n.ctx, id)
	if err != nil {
		return false
	}
	if key != "" {
		ctx, done := context.WithTimeout(n.ctx, hubCallTimeout)
		first, found, err := n.hub.ByIdem(ctx, n.qualify(local.From), key)
		done()
		switch {
		case err != nil && time.Since(local.CreatedAt) < hubAbsence:
			// No answer, or a hub without the lookup (an older version answers 404 for any
			// path): it may hold the send under an earlier id. Past the wait, the disown below
			// asks by the key too (or, a hub without either, the copy ends as before).
			return false
		case err != nil:
		case found && first.ID != id:
			_, err := n.takeHubRepeat(n.ctx, id, first)
			return err == nil
		case found:
			return false // there now under this id: the next try finds it
		}
	}
	if time.Since(local.CreatedAt) < hubAbsence {
		return false // the send may still arrive: absence is asked about only later
	}
	return n.disownForward(id, local, key)
}

// foundOnHub settles a forwarding copy the hub has (hm): the copy follows it; cancelled here
// meanwhile, the cancel goes to the hub first, and a hub that had already ended the message gives
// the copy its outcome.
func (n *Node) foundOnHub(id string, local, hm store.Message) bool {
	wanted, err := n.st.CancelWanted(n.ctx, id)
	if err != nil {
		return false
	}
	if wanted && !local.State.Final() {
		ctx, done := context.WithTimeout(n.ctx, hubCallTimeout)
		_, err := n.hub.Cancel(ctx, id)
		done()
		if err != nil && !isFinalErr(err) {
			return false // the cancel did not reach the hub: again later
		}
		// Cancelled there, or ended before the cancel: the hub's outcome is the message's.
		ok, err := n.st.CancelSettled(n.ctx, id, originForwarding, originRemote)
		if err != nil {
			return false
		}
		if ok {
			n.notify()
			n.startFollow(id)
		}
		return true
	}
	if local.State.Final() { // cancelled by an earlier version while forwarding
		ctx, done := context.WithTimeout(n.ctx, hubCallTimeout)
		_, err := n.hub.Cancel(ctx, id)
		done()
		switch {
		case err == nil:
			_, err = n.st.SetOriginIf(n.ctx, id, originForwarding, originRemote)
			return err == nil
		case isFinalErr(err):
			// Ended on the hub before the cancel: its outcome is the message's.
			ok, err := n.st.ReopenForward(n.ctx, id, originForwarding, originRemote)
			if err != nil {
				return false
			}
			if ok {
				n.notify()
				n.startFollow(id)
			}
			return true
		}
		return false // the cancel did not reach the hub: again later
	}
	ok, err := finishForward(n.st, n.ctx, id, originForwarding, originRemote, hm)
	if err != nil || !ok {
		return false // a write failed, or the copy changed (cancelled): again, from what it is then
	}
	n.notify()
	n.startFollow(id)
	return true
}

// resumeForwards resolves the messages the last process stored to send through the hub and did
// not learn the fate of, cancels it did not carry to the hub included.
func (n *Node) resumeForwards() {
	for after := ""; n.ctx.Err() == nil; {
		page, err := n.st.OfOrigin(n.ctx, originForwarding, after, store.MaxPage)
		if err != nil || len(page) == 0 {
			return
		}
		for _, id := range page {
			n.resolveLater(id)
		}
		after = page[len(page)-1]
	}
}

// cancelForwarding asks for a copy still forwarding to be cancelled, only while it is (a send
// finishing at the same time makes it the hub's: then the hub is asked first, as for any remote
// copy). done: the cancel is recorded, and the copy, still unfinished, ends when the hub's verdict
// is known; otherwise m is the copy as it is now.
func (n *Node) cancelForwarding(ctx context.Context, p Principal, m store.Message) (store.Message, bool, error) {
	ok, err := n.st.WantCancel(ctx, m.ID, originForwarding)
	if err != nil {
		return m, false, err
	}
	if ok {
		n.resolveLater(m.ID) // the cancel goes to the hub (or the hub is found not to have it)
		cur, err := n.Get(ctx, p, m.ID)
		return cur, true, err
	}
	cur, err := n.Get(ctx, p, m.ID)
	return cur, false, err
}

// cancelThrough sends the hub a cancel asked for on a forwarding copy (id) to the message the hub
// kept for it (hubID), before the copy is replaced (every try: a cancel the hub has already is a
// conflict there, nothing more); the resolver confirms it once the copy is replaced.
func (n *Node) cancelThrough(id, hubID string) {
	local, err := n.st.Get(n.ctx, id)
	if err != nil || local.Origin != originForwarding {
		return
	}
	if wanted, err := n.st.CancelWanted(n.ctx, id); err != nil || (!wanted && !local.State.Final()) {
		return
	}
	ctx, done := context.WithTimeout(n.ctx, hubCallTimeout)
	_, _ = n.hub.Cancel(ctx, hubID)
	done()
}
