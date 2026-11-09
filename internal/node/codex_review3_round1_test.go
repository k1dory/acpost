package node

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A turn naming by id the session the turn that sent it runs in (task T's first turn sends itself
// --session S --wait) is refused at once: the sender holds the session until this answer comes.
func TestSessionOfItsOwnSenderIsRefused(t *testing.T) {
	n := roundNode(t, nil, "concurrency: 2")
	parent, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 5000", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	sid := storedSession(t, n, "task:T")
	var pa Principal
	found := false
	for deadline := time.Now().Add(10 * time.Second); !found; {
		for _, s := range n.acp["fake"].slots {
			if p, ok := n.PrincipalForToken(s.token); ok && n.currentOf(p) == parent.ID {
				pa, found = p, true
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("task T's turn never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	child, _, err := n.Send(ctx, pa, SendRequest{To: "fake", Body: "echo hi", Session: sid})
	if err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, child.ID, 3*time.Second)
	if err != nil || w.State != store.Failed || w.ErrorCode != "refused" {
		t.Fatalf("--session %s from its own turn: %s %s %q %v (want refused at once, not a wait for the sender)", sid, w.State, w.ErrorCode, w.Error, err)
	}
}

// The last attempt ends before it reads the turn (sandboxd's history answers 503): the usage an
// earlier attempt read and counted goes among the message's events before the message is failed,
// so the app and the sender's node get it, not only this node's count.
func TestSandboxCountedUsageIsPublishedAtFailure(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "acpost.db")
	n, _ := sandboxNodeOn(t, breakingSandbox(t, true), fmt.Sprintf("store: %q\nlimits: {max_attempts: 2}\n", "sqlite://"+path))
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)") // the node writes as it starts
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	m, _, err := n.Send(ctx, me, SendRequest{To: "cloud", Body: "count me"})
	if err != nil {
		t.Fatal(err)
	}
	seen := watchFinalUsage(t, db, m.ID)
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Failed || w.Attempts != 2 {
		t.Fatalf("%s %s %q attempts=%d %v (want failed after 2)", w.State, w.ErrorCode, w.Error, w.Attempts, err)
	}
	if counted, events, early := seen(); counted != 1 || events != 1 || early {
		t.Fatalf("final: usage counted %d, usage events %d (want 1, 1); seen final before its usage event: %v", counted, events, early)
	}
}
