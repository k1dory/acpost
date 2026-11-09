package store

import (
	"errors"
	"strings"
)

// CopyOrigins are the origins of a joined node's local copies of messages its hub holds: sent
// through the hub (remote), on their way (forwarding), or not sent (unsent). Such a copy is the
// hub's work, whatever its recipient reads: nothing here claims it, counts it in a queue,
// requeues it or reaps it. (A copy stored before the copy kept the sender's address names the
// hub's agent like a local one: agent:bot.)
var CopyOrigins = []string{"remote", "forwarding", "unsent"}

// localWork is the condition every query over "this node's work for an agent" adds.
var localWork = "origin NOT IN ('" + strings.Join(CopyOrigins, "', '") + "')"

// ErrStaleDelivery: a delivery of a message older than the one its copy here already is (the
// hub's attempt number is lower) — the hub handed it again since, and that delivery owns the copy.
var ErrStaleDelivery = errors.New("an older delivery of the message than the one its copy is")

// queueOf is the queue a new message with origin joins, for InsertLimited's limit: a copy stored to
// be sent through the hub waits among the copies the hub has not answered for yet (a hub away
// does not let them grow without bound); any other message among this node's work.
func queueOf(origin string) string {
	if origin == "forwarding" {
		return "origin = 'forwarding'"
	}
	return localWork
}
