package node

// Regression tests for the first review of PR #7 that need what this round added (a store seam,
// the client's key).

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// One failed write when the hub has taken the message does not leave the copy stuck: it is
// finished by the resolver.
func TestForwardRecoversFromFailedWrite(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	old := finishForward
	var failed atomic.Bool
	finishForward = func(s *store.Store, c context.Context, id, from, to string, sent store.Message) (bool, error) {
		if failed.CompareAndSwap(false, true) {
			return false, errors.New("disk I/O error")
		}
		return old(s, c, id, from, to, sent)
	}
	t.Cleanup(func() { finishForward = old })
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.state = store.Queued })
	n.hub = f.client(t)
	m, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work"})
	if err != nil {
		t.Fatal(err)
	}
	eventually(t, "the copy is the hub's", func() bool {
		got, _ := n.st.Get(ctx, m.ID)
		return got.Origin == originRemote
	})
}

// The client's handoff request carries the key to the node's.
func TestHandoffKeyThroughClient(t *testing.T) {
	b, _ := json.Marshal(client.HandoffRequest{CloudAgent: "assistant", Cwd: "/r", Idem: "h-9"})
	var r HandoffRequest
	if err := json.Unmarshal(b, &r); err != nil || r.Idem != "h-9" {
		t.Fatalf("%s → %+v %v", b, r, err)
	}
}
