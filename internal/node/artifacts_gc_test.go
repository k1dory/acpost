package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A thread's root collected by retention before its later messages (review round 5): their
// artifacts still say the chat's directory — their own message's, else another kept one's.
func TestArtifactsCwdOutlivesTheRoot(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	lim := store.FileLimits{Count: 4, Size: 1 << 20}
	root := &store.Message{ID: store.NewID(), From: "user:tester", To: "agent:bot", Body: "start", Meta: map[string]string{metaCwd: "/project-b"}}
	if _, _, err := n.st.Insert(ctx, root); err != nil {
		t.Fatal(err)
	}
	c, _, _ := n.st.Claim(ctx, "agent:bot", time.Minute)
	if err := n.st.Complete(ctx, root.ID, c.LeaseID, store.Done, "ok", "", ""); err != nil {
		t.Fatal(err)
	}
	withCwd := &store.Message{ID: store.NewID(), From: "user:tester", To: "agent:bot", Body: "more", Thread: root.ID, Meta: map[string]string{metaSessionCwd: "/project-b"}}
	bare := &store.Message{ID: store.NewID(), From: "user:tester", To: "agent:bot", Body: "and more", Thread: root.ID}
	for _, m := range []*store.Message{withCwd, bare} {
		if _, _, err := n.st.Insert(ctx, m); err != nil {
			t.Fatal(err)
		}
		c, _, _ := n.st.Claim(ctx, "agent:bot", time.Minute)
		if err := n.st.AttachReply(ctx, c.ID, c.LeaseID, []store.File{{Name: c.ID + ".md", Type: "text/markdown", Data: []byte("x")}}, lim); err != nil {
			t.Fatal(err)
		}
	}
	if gone, err := n.st.GC(ctx, time.Now().Add(time.Second)); err != nil || gone != 1 {
		t.Fatalf("collected %d %v", gone, err)
	}
	if _, err := n.st.Get(ctx, root.ID); err == nil {
		t.Fatal("the root is still there")
	}
	got, err := n.Artifacts(ctx, me, store.ArtifactQuery{})
	if err != nil || len(got.Items) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	for _, a := range got.Items {
		if a.Thread != root.ID || a.Cwd != "/project-b" {
			t.Errorf("%s: thread %s, directory %q", a.Name, a.Thread, a.Cwd)
		}
	}
}
