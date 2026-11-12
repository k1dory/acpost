//go:build race

package store

// slow is how much longer a timed bound allows under the race detector (CI runs -race).
const slow = 20
