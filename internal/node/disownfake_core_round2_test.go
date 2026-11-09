package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A disown that finds the send arrived meanwhile: the copy follows the hub's message.
func TestDisownFoundCopyFollows(t *testing.T) {
	fastHub(t, 30*time.Millisecond)
	n := newNode(t, hubCfg)
	f := newFakeHub(t, "")
	f.set(func(f *fakeHub) { f.down = true })
	n.hub = f.client(t)
	if _, _, err := n.Send(ctx, me, SendRequest{To: "agent:far@other", Body: "work", IdemKey: "d1"}); err == nil {
		t.Fatal("hub away, the send succeeded")
	}
	m, err := n.st.ByIdem(ctx, me.Address(), "d1")
	if err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeHub) {
		f.down, f.missing = false, true
		f.disownHas = &store.Message{ID: m.ID, From: "user:tester@edge", To: "agent:far@other", Body: "work", Thread: m.ID, State: store.Running}
	})
	eventually(t, "the copy follows the hub's message", func() bool {
		got, _ := n.st.Get(ctx, m.ID)
		return got.Origin == originRemote && !got.State.Final()
	})
}
