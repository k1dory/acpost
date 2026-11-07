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
	n.runSchedules(ctx, time.Now())
	n.runSchedules(ctx, time.Now())
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
	// Run now, then delete.
	if m, err := n.RunSchedule(ctx, me, sc.ID); err != nil || m.ID == "" {
		t.Fatalf("%+v %v", m, err)
	}
	if err := n.DeleteSchedule(ctx, me, sc.ID); err != nil {
		t.Fatal(err)
	}
}
