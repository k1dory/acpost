package node

import "time"

// hubRenew is how often a relay renews the hub's lease on a delivered message: a third of it
// (a variable so tests need not wait minutes).
var hubRenew = hubLease / 3

// hubRetry is the first pause before a reply the hub did not take is tried again; it doubles, up
// to hubRenew.
var hubRetry = time.Second

// Deadlines of one call to the hub from a loop of the node: a hub that accepts and never answers
// (a hung handler, a connection lost without a reset) holds the loop no longer than this. A long
// poll has its wait on top; an event stream is opened again after hubStream (the hub's pings are
// not seen from here).
var (
	hubCallTimeout = 10 * time.Second
	hubClaimWait   = 30 * time.Second
	hubStream      = 5 * time.Minute
)

const hubReplyTimeout = time.Minute // a reply carries its files

// hubAbsence is how long after a forwarded message was stored the hub's "no such message" is
// taken for "not sent": a send still on its way may arrive before that.
var hubAbsence = 30 * time.Second

// hubFilesTimeout bounds one download of a message's files the hub kept (a forward's recovery).
var hubFilesTimeout = time.Minute
