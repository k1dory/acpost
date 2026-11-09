package node

import (
	"database/sql"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// An event whose write fails in the middle of a page is not skipped: the outcome waits, and the
// next try copies it (the cursor did not move past it on the next event's write).
func TestHubEventWriteFailureIsNotSkipped(t *testing.T) {
	dir := t.TempDir() + "/s" // (made private by the store)
	n, st := fileNode(t, "store: sqlite://"+dir+"/store.db\n"+hubCfg)
	f := newFakeHub(t, "")
	m, _, err := st.Insert(ctx, &store.Message{ID: store.NewID(), From: me.Address(), To: "agent:far@other", Body: "w", Origin: originRemote})
	if err != nil {
		t.Fatal(err)
	}
	f.set(func(f *fakeHub) {
		f.state = store.Done
		for i, d := range []string{"one", "two", "three"} {
			f.events = append(f.events, store.Event{Seq: int64(i + 1), Kind: "message", Data: d})
		}
	})
	n.hub = f.client(t)
	// The store refuses the second event, as a busy or full disk would.
	db, err := sql.Open("sqlite", "file:"+dir+"/store.db")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER fail_two BEFORE INSERT ON events WHEN NEW.data = 'two' BEGIN SELECT RAISE(ABORT, 'injected'); END`); err != nil {
		t.Fatal(err)
	}
	if n.copyHubEvents(m.ID) {
		t.Error("the events were taken as copied with one of them not written")
	}
	if _, err := db.Exec(`DROP TRIGGER fail_two`); err != nil {
		t.Fatal(err)
	}
	if !n.copyHubEvents(m.ID) {
		t.Fatal("not copied on the next try")
	}
	evs, err := st.Events(ctx, m.ID, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range evs {
		got = append(got, e.Data)
	}
	if len(got) != 3 || got[0] != "one" || got[1] != "two" || got[2] != "three" {
		t.Fatalf("the copy has %q, want one two three", got)
	}
}
