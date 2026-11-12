package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// Long threads whose messages say no directory (review round 6): the page's directories come
// from one look for all its threads, not a walk of each thread's messages.
func TestArtifactsDirsOfLongThreads(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	for range 3 {
		root := &store.Message{ID: store.NewID(), From: "user:tester", To: "agent:bot", Body: "start"}
		if _, _, err := n.st.Insert(ctx, root); err != nil {
			t.Fatal(err)
		}
		for range 2000 {
			m := &store.Message{ID: store.NewID(), From: "user:tester", To: "agent:other", Body: "a body the listing does not read", Thread: root.ID}
			if _, _, err := n.st.Insert(ctx, m); err != nil {
				t.Fatal(err)
			}
		}
		c, _, _ := n.st.Claim(ctx, "agent:bot", time.Minute)
		if err := n.st.AttachReply(ctx, c.ID, c.LeaseID, []store.File{{Name: "r.md", Data: []byte("x")}}, store.FileLimits{Count: 4, Size: 1 << 20}); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	for range 5 {
		got, err := n.Artifacts(ctx, me, store.ArtifactQuery{})
		if err != nil || len(got.Items) != 3 {
			t.Fatalf("%+v %v", got, err)
		}
		for _, a := range got.Items {
			if a.Cwd != "" {
				t.Fatalf("a directory from nowhere: %+v", a)
			}
		}
	}
	took := time.Since(start)
	t.Logf("five listings: %v", took)
	if took > time.Second {
		t.Fatalf("five listings of three artifacts took %v", took)
	}
}
