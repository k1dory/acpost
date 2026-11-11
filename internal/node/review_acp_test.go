package node

// Regression tests from the Codex review (gpt-6-astra, 2026-11-05).

import (
	"fmt"
	acp "github.com/coder/acp-go-sdk"
	"github.com/k1dory/acpost/internal/store"
	"os"
	"sync"
	"testing"
	"time"
)

func TestACPDoesNotExecuteReapedClaim(t *testing.T) {
	n := acpNode(t, "preamble: false")
	a := n.acp["fake"]

	unlock := a.lockKey("task:T")
	var once sync.Once
	release := func() { once.Do(unlock) }
	defer release()

	m, _, err := n.Send(ctx, me, SendRequest{
		To: "fake", Body: "count", Task: "T",
	})
	if err != nil {
		t.Fatal(err)
	}

	var claimed store.Message
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		claimed, err = n.st.Get(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if claimed.State == store.Running {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if claimed.State != store.Running {
		t.Fatal("worker never claimed the message")
	}

	// Advance the reaper's clock past the waiting claim's lease.
	if _, err := n.st.ReapLeases(ctx,
		claimed.LeaseUntil.Add(time.Millisecond), 2); err != nil {
		t.Fatal(err)
	}
	release()

	got, err := n.Wait(ctx, me, m.ID, 5*time.Second)
	if err != nil || got.State != store.Done || got.Reply != "1" {
		t.Fatalf("stale claim executed: state=%s reply=%q attempts=%d err=%v",
			got.State, got.Reply, got.Attempts, err)
	}
}

func TestACPSessionStateAcrossSlots(t *testing.T) {
	n := acpNode(t, "preamble: false\nconcurrency: 2")
	a := n.acp["fake"]

	// Force a slot order that the current scheduler permits.
	for i, index := range []int{0, 1, 0} {
		slot := a.slots[index]
		p, err := slot.ensureProc(ctx)
		if err != nil {
			t.Fatal(err)
		}
		sid, err := slot.session(ctx, p, "task:T")
		if err != nil {
			t.Fatal(err)
		}
		turn := p.cl.begin(sid, "session-regression", 1024, 16, 1<<20)
		_, err = p.conn.Prompt(ctx, acp.PromptRequest{
			SessionId: sid,
			Prompt:    []acp.ContentBlock{acp.TextBlock("count")},
		})
		p.cl.end(sid)
		if err != nil {
			t.Fatal(err)
		}
		if got, want := turn.reply("last"), fmt.Sprint(i+1); got != want {
			t.Fatalf("slot %d returned %q, want %q", index, got, want)
		}
	}
}

func TestGCDoesNotForgetRecentlyUsedSession(t *testing.T) {
	n := acpNode(t, "preamble: false")
	if got := ask(t, n, SendRequest{Body: "count", Task: "T"}); got.Reply != "1" {
		t.Fatal(got.Reply)
	}
	time.Sleep(5 * time.Millisecond)
	cutoff := time.Now()

	if got := ask(t, n, SendRequest{Body: "count", Task: "T"}); got.Reply != "2" {
		t.Fatal(got.Reply)
	}
	if _, err := n.st.GC(ctx, cutoff); err != nil {
		t.Fatal(err)
	}
	n.acp["fake"].slots[0].stopProc()

	if got := ask(t, n, SendRequest{Body: "count", Task: "T"}); got.Reply != "3" {
		t.Fatalf("recently used session forgotten after GC: %q", got.Reply)
	}
}

func TestRestartHonorsMaxAttempts(t *testing.T) {
	n := newNode(t, fmt.Sprintf(`
limits: {max_attempts: 3}
agents:
  fake:
    driver: inbox
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1"}
    preamble: false
`, os.Args[0]))

	m, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "count"})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 3; attempt++ {
		c, ok, err := n.st.Claim(ctx, "agent:fake", time.Hour)
		if err != nil || !ok {
			t.Fatal(ok, err)
		}
		if attempt < 3 {
			if _, err := n.st.Release(ctx, c.ID, c.LeaseID, "agent_crashed", "interrupted", 3); err != nil {
				t.Fatal(err)
			}
		}
	}

	n.Close()
	n.cfg.Agents["fake"].Driver = "acp"
	restarted, err := New(n.cfg, n.st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()

	got, err := restarted.Wait(ctx, me, m.ID, 5*time.Second)
	if err != nil || got.State != store.Failed || got.Attempts != 3 {
		t.Fatalf("restart exceeded attempts: state=%s attempts=%d err=%v",
			got.State, got.Attempts, err)
	}
}
