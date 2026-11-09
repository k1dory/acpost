package node

import (
	"slices"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// The store keeps a joined node's copies out of its queues by their origins: every origin this
// package gives a copy of a message its hub holds is one of them, and no delivery's is.
func TestCopyOriginsMatchStore(t *testing.T) {
	for _, o := range []string{originRemote, originForwarding, originUnsent} {
		if !slices.Contains(store.CopyOrigins, o) {
			t.Errorf("origin %q is a copy's but the store counts it as local work", o)
		}
	}
	for _, o := range store.CopyOrigins {
		if len(o) >= len(originHub) && o[:len(originHub)] == originHub {
			t.Errorf("origin %q of a delivery is kept out of the queues", o)
		}
	}
}
