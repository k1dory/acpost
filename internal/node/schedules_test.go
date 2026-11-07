package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A schedule sends its task when due, once (the next run moves on), from the node's user, in its
// directory; a bad cron, a client, are refused.
func TestSchedules(t *testing.T) {
	n := acpNode(t, "")
	dir := t.TempDir()
	if _, err := n.PutSchedule(ctx, me, store.Schedule{Name: "x", Cron: "not cron", Agent: "fake", Body: "echo hi"}); code(err) != "invalid" {
		t.Fatalf("bad cron: %v", err)
	}
	if _, err := n.PutSchedule(ctx, Principal{Kind: "client", Name: "ci"}, store.Schedule{Name: "x", Cron: "@daily", Agent: "fake", Body: "x"}); code(err) != "refused" {
		t.Fatalf("a client: %v", err)
	}
	sc, err := n.PutSchedule(ctx, me, store.Schedule{Name: "nightly", Cron: "0 3 * * *", Agent: "fake", Body: "cwd", Cwd: dir, Enabled: true})
	if err != nil || sc.Agent != "agent:fake" || sc.NextRun.Before(time.Now()) {
		t.Fatalf("%+v %v", sc, err)
	}
	// Due now: the tick sends it, once.
	sc.NextRun = time.Now().Add(-time.Minute).UTC().Truncate(time.Millisecond)
	if err := n.st.PutSchedule(ctx, sc); err != nil {
		t.Fatal(err)
	}
	tick(t, n)
	tick(t, n)
	list, _ := n.Schedules(ctx, me)
	if len(list) != 1 || list[0].LastMsg == "" || list[0].LastErr != "" || !list[0].NextRun.After(time.Now()) {
		t.Fatalf("%+v", list)
	}
	w, err := n.Wait(ctx, me, list[0].LastMsg, 30*time.Second)
	if err != nil || w.State != store.Done || w.Reply == "" || w.Meta["schedule"] != sc.ID {
		t.Fatalf("%+v %v", w, err)
	}
	page, _, _ := n.st.List(ctx, store.Filter{To: "agent:fake", Limit: 10})
	if len(page) != 1 {
		t.Fatalf("sent %d times", len(page))
	}
	// A crash after the claim and before the send leaves it pending: the next pass sends it; and
	// one already sent (the crash after the send) is not sent again.
	occ := list[0].NextRun
	if ok, err := n.st.TakeDue(ctx, sc.ID, occ, occ.Add(24*time.Hour)); !ok || err != nil {
		t.Fatalf("claim: %v %v", ok, err)
	}
	tick(t, n)
	list, _ = n.Schedules(ctx, me)
	again, err := n.sendScheduled(ctx, list[0], occ)
	if err != nil || again.ID != list[0].LastMsg {
		t.Fatalf("a resent occurrence: %s, first %s (%v)", again.ID, list[0].LastMsg, err)
	}
	if page, _, _ := n.st.List(ctx, store.Filter{To: "agent:fake", Limit: 10}); len(page) != 2 {
		t.Fatalf("a pending occurrence: sent %d times in all", len(page))
	}
	if list, _ = n.Schedules(ctx, me); !list[0].Pending.IsZero() {
		t.Fatalf("still pending: %+v", list[0])
	}
	// A date that never comes is refused.
	if _, err := n.PutSchedule(ctx, me, store.Schedule{Name: "x", Cron: "0 9 31 2 *", Agent: "fake", Body: "x", Enabled: true}); code(err) != "invalid" {
		t.Fatalf("31 February: %v", err)
	}
	// Run now notes when, the next run unchanged; then delete.
	before := list[0]
	time.Sleep(5 * time.Millisecond)
	if m, err := n.RunSchedule(ctx, me, sc.ID); err != nil || m.ID == "" {
		t.Fatalf("%+v %v", m, err)
	}
	if list, _ = n.Schedules(ctx, me); !list[0].LastRun.After(before.LastRun) || !list[0].NextRun.Equal(before.NextRun) {
		t.Fatalf("run now: %+v, before %+v", list[0], before)
	}
	if err := n.DeleteSchedule(ctx, me, sc.ID); err != nil {
		t.Fatal(err)
	}
}

// tick runs a schedules pass and waits for it.
func tick(t *testing.T, n *Node) {
	t.Helper()
	n.runSchedules(ctx, time.Now())
	for deadline := time.Now().Add(30 * time.Second); n.schedBusy.Load(); time.Sleep(5 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("a schedules pass did not end")
		}
	}
}
