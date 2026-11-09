package store

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// cancelledAfter is a context that says it is done from its (after+1)th look at Done on: it
// stands for a cancellation that lands at any point within the call.
type cancelledAfter struct {
	context.Context
	after, looks int32
	done         chan struct{}
}

func (c *cancelledAfter) Done() <-chan struct{} {
	if atomic.AddInt32(&c.looks, 1) > c.after {
		return c.done
	}
	return nil
}

func (c *cancelledAfter) Err() error {
	if atomic.LoadInt32(&c.looks) > c.after {
		return context.Canceled
	}
	return nil
}

// However late the caller's ctx is cancelled while the short wait is set, the writer connection
// goes back to the pool with its own busy_timeout.
func TestShortWaitRestoresTheTimeout(t *testing.T) {
	s := openTest(t, Options{})
	bg := context.Background()
	closed := make(chan struct{})
	close(closed)
	for after := int32(0); after < 64; after++ {
		c, err := s.w.Conn(bg)
		if err != nil {
			t.Fatal(err)
		}
		shortWait(&cancelledAfter{Context: bg, after: after, done: closed}, c, 250*time.Millisecond, func() {})
		var ms int
		if err := c.QueryRowContext(bg, "PRAGMA busy_timeout").Scan(&ms); err != nil {
			t.Fatal(err)
		}
		c.Close()
		if ms != 10000 {
			t.Fatalf("cancelled after %d looks: busy_timeout left at %d", after, ms)
		}
	}
}
