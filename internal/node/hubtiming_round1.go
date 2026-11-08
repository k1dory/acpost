package node

// hubRenew is how often a relay renews the hub's lease on a delivered message: a third of it
// (a variable so tests need not wait minutes).
var hubRenew = hubLease / 3
