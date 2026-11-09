//go:build darwin

package main

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// The app's node got ready, then stopped (its listener failed): Status, called at once from the
// window's timer and its "Try again", starts one node again. It used to check the dead address
// for ever: startErr was cleared by the good start, and only a failed start retried.
func TestStatusRestartsAnEndedNode(t *testing.T) {
	var starts atomic.Int32
	die := make(chan struct{})
	ownNode(t, func(ctx context.Context, ready func()) error {
		first := starts.Add(1) == 1
		ready()
		if first {
			select {
			case <-die:
				return errors.New("listen: address in use")
			case <-ctx.Done():
				return nil
			}
		}
		<-ctx.Done()
		return nil
	})
	n := &Node{emitFn: func(string, any) {}}
	n.startErr = "starting"
	n.Status()
	if starts.Load() != 1 {
		t.Fatalf("%d starts, want 1", starts.Load())
	}
	close(die)
	eventually(t, 2*time.Second, "the first node ends", func() bool { return n.ownNodeEnded() })
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); n.Status() }()
	}
	wg.Wait()
	if s := starts.Load(); s != 2 {
		t.Errorf("%d starts, want 2 (one again)", s)
	}
	n.ServiceShutdown()
	if n.ownNodeEnded() {
		t.Error("a node stopped by the app quitting is to start again")
	}
}
