//go:build race

package store

// raceOn: the race detector slows the store's stress tests about tenfold; scale cuts their sizes.
const raceOn = true
