package node

// The hub's events copied onto a forwarded message resume from the hub's number of the last one
// taken, not from the local count (PR #6's cursor): the two differ once this node keeps fewer.

import (
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// Local limit 3, the hub keeps more: after its five events the copy has four (three and the
// truncated mark), and copying again, as after a restart, asks the hub from its fifth.
func TestHubEventsResumeFromHubNumber(t *testing.T) {
	n := newNode(t, strings.Replace(hubCfg, "limits: {", "limits: {max_events_per_message: 3, ", 1))
	f := newFakeHub(t, "")
	m, _, err := n.st.Insert(ctx, &store.Message{ID: store.NewID(), From: me.Address(), To: "agent:far@other", Body: "w", Origin: originRemote})
	if err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeHub) {
		f.state = store.Running
		for i := 1; i <= 5; i++ {
			f.events = append(f.events, store.Event{Seq: int64(i), Kind: "tool_call", Data: "{}"})
		}
	})
	n.hub = f.client(t)
	if !n.copyHubEvents(m.ID) {
		t.Fatal("not copied")
	}
	if c, _ := n.st.EventCount(ctx, m.ID); c >= 5 {
		t.Fatalf("the copy keeps %d events: the limit does not make the counts differ", c)
	}
	f.set(func(f *fakeHub) { f.events = append(f.events, store.Event{Seq: 6, Kind: "tool_call", Data: "{}"}) })
	if !n.copyHubEvents(m.ID) { // a restart: nothing in memory, the store says where to resume
		t.Fatal("not copied")
	}
	if from := f.get().eventsFrom; len(from) != 2 || from[1] != 5 {
		t.Fatalf("the hub was asked for its events after %v; after a restart it is to be 5", from)
	}
	if c, _ := n.st.HubCursor(ctx, m.ID); c != 6 {
		t.Fatalf("hub cursor %d", c)
	}
}
