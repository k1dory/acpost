package node

import (
	"runtime"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

// A node closed and let go is collected: nothing of the package keeps it (the desktop starts an
// embedded node again on every restart of its own).
func TestClosedNodeNotRetained(t *testing.T) {
	gone := make(chan struct{})
	func() {
		cfg, err := config.Parse([]byte(hubCfg), ".yaml")
		if err != nil {
			t.Fatal(err)
		}
		cfg.Store = "memory:"
		st, err := store.Open(cfg.Store, StoreOptions(cfg))
		if err != nil {
			t.Fatal(err)
		}
		n, err := New(cfg, st, nil)
		if err != nil {
			t.Fatal(err)
		}
		if err := n.Start(ctx); err != nil {
			t.Fatal(err)
		}
		n.waits() // what a turn waiting for a message records
		n.Close()
		st.Close()
		runtime.AddCleanup(n, func(ch chan struct{}) { close(ch) }, gone)
	}()
	deadline := time.Now().Add(10 * time.Second)
	for {
		runtime.GC()
		select {
		case <-gone:
			return
		case <-time.After(50 * time.Millisecond):
		}
		if time.Now().After(deadline) {
			t.Fatal("a closed node is still held")
		}
	}
}
