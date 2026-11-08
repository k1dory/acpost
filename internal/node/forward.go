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
// a repeat finds it without the hub and a request that ends after the hub took the message still
// leaves it. Then:
//
//	forwarding  the hub's answer is not known yet: resolveForward learns it from the hub
//	remote      the hub has it: the copy follows the hub's (followSweep, follow)
//	unsent      not sent for certain (refused, or the hub never got it); its key is free again
//
// A copy cancelled (or ended) here while still forwarding keeps that origin until the cancel has
// reached the hub too.

const (
	originForwarding = "forwarding"
	originUnsent     = "unsent"
)

// finishForward is the store's FinishForward (a test makes one write fail).
var finishForward = (*store.Store).FinishForward

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
	got, created, err := n.st.Insert(lctx, &pending)
	if err != nil {
		return store.Message{}, false, n.fileErr(err, m.ID)
	}
	if !created {
		return hideLease(got), false, nil // a repeat of the key, sent at the same time
	}
	sent, created, err := n.hub.Send(ctx, req, m.IdemKey)
	if err != nil {
		if hubRefused(err) {
			n.notSent(lctx, m.ID, hubErr(err))
		} else {
			n.resolveLater(m.ID) // the hub may have it: asked (a node closing asks at its next start)
		}
		return store.Message{}, false, hubErr(err)
	}
	if !created && sent.ID != m.ID {
		copied, err := n.takeHubRepeat(ctx, m.ID, sent)
		if err != nil {
			n.resolveLater(m.ID)
			return store.Message{}, false, err
		}
		return hideLease(copied), false, nil
	}
	if ok, err := finishForward(n.st, lctx, m.ID, originForwarding, originRemote, sent); err != nil || !ok {
		// The hub has it, the copy is not marked so (a write failed, or it was cancelled
		// meanwhile): the resolver finishes it, carrying a cancel to the hub.
		n.resolveLater(m.ID)
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
// for the same key before (sent): its first send, whose local copy is gone or was never made.
func (n *Node) takeHubRepeat(ctx context.Context, id string, sent store.Message) (store.Message, error) {
	lctx := context.WithoutCancel(ctx)
	pending, err := n.st.Get(lctx, id)
	if err != nil {
		return store.Message{}, err
	}
	key, err := n.st.IdemKeyOf(lctx, id)
	if err != nil {
		return store.Message{}, err
	}
	var files []store.File
	if listed := pick(sent.Files, false); len(listed) > 0 {
		got, err := n.fetchFiles(ctx, sent.ID, false, listed, "the message")
		var e *Error
		if err != nil && !errors.As(err, &e) {
			err = hubErr(err)
		}
		if err != nil {
			return store.Message{}, err
		}
		files = got
	}
	if err := n.st.DropForward(lctx, id, originForwarding); err != nil {
		return store.Message{}, err
	}
	local := &store.Message{ID: sent.ID, From: pending.From, To: sent.To, Task: sent.Task, Thread: sent.Thread, Parent: sent.Parent,
		Depth: sent.Depth, Meta: sent.Meta, Body: sent.Body, ExpiresAt: sent.ExpiresAt, Origin: originRemote, Files: files, IdemKey: key}
	got, _, err := n.st.Insert(lctx, local)
	if err != nil {
		if existing, gerr := n.st.Get(lctx, sent.ID); gerr == nil {
			return existing, nil
		}
		return store.Message{}, err
	}
	n.notify()
	n.startFollow(sent.ID)
	return got, nil
}

// notSent marks a forwarded message as not sent for certain.
func (n *Node) notSent(ctx context.Context, id string, why error) bool {
	ok, err := n.st.ForwardNotSent(ctx, id, originForwarding, originUnsent, "not sent through the hub: "+why.Error())
	if err != nil {
		return false
	}
	if ok {
		n.notify()
	}
	return true
}

// resolveLater starts resolveForward for a message, unless one runs for it already.
func (n *Node) resolveLater(id string) {
	if _, busy := n.resolving.LoadOrStore(id, true); busy {
		return
	}
	if !n.goWork(func() {
		defer n.resolving.Delete(id)
		n.resolveForward(id)
	}) {
		n.resolving.Delete(id)
	}
}

// resolveForward learns from the hub what became of a message stored to be sent through it (the
// hub did not answer, the request ended, the node stopped, the copy was cancelled meanwhile),
// trying again with a growing pause until it knows. It does not send the message again: its
// sender was told the send failed.
func (n *Node) resolveForward(id string) {
	for pause := hubRetry; n.ctx.Err() == nil; pause = min(2*pause, time.Minute) {
		select {
		case <-n.ctx.Done():
			return
		case <-time.After(pause):
		}
		if n.resolveOnce(id) {
			return
		}
	}
}

// resolveOnce is one try of resolveForward; it reports whether the message is settled.
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
		first, err := n.hub.ByIdem(ctx, n.qualify(local.From), key)
		done()
		switch {
		case err == nil:
			if local.State.Final() {
				return n.foundOnHub(first.ID, local, first) && n.notSent(n.ctx, id, errors.New("cancelled before the hub had it"))
			}
			_, err := n.takeHubRepeat(n.ctx, id, first)
			return err == nil
		case errors.As(err, &ce) && (ce.Code == "not_found" || ce.Status == 404 || ce.Status == 405):
			// None (or a hub without the lookup): not sent.
		default:
			return false
		}
	}
	return n.notSent(n.ctx, id, errors.New("the hub did not get it; send again"))
}

// foundOnHub settles a forwarding copy the hub has (hm, under hm.ID): the copy follows it, or, ended
// here meanwhile, the hub's is cancelled first.
func (n *Node) foundOnHub(id string, local, hm store.Message) bool {
	if local.State.Final() {
		ctx, done := context.WithTimeout(n.ctx, hubCallTimeout)
		_, err := n.hub.Cancel(ctx, hm.ID)
		done()
		if err != nil && !isFinalErr(err) {
			return false // the cancel did not reach the hub: again later
		}
		if hm.ID != id {
			return true
		}
		_, err = n.st.SetOriginIf(n.ctx, id, originForwarding, originRemote)
		return err == nil
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
