package node

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// X13: what a turn says is recorded under its attempt's lease: once the lease is reaped and the
// message taken again, the old attempt's late words are not the new attempt's events.
func TestLateEventsOfALostAttemptAreDropped(t *testing.T) {
	n := newNode(t, fmt.Sprintf(`
agents:
  fake: {driver: acp, command: [%q], env: {ACPOST_FAKE_AGENT: "1"}, preamble: false}
  bot: {driver: inbox}
`, os.Args[0]))
	m, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	first, ok, err := n.st.Claim(ctx, "agent:bot", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	c := &acpClient{slot: n.acp["fake"].slots[0], turns: map[acpSessionID]*acpTurn{}}
	old := c.begin("s-old", m.ID, 1<<20, 1, 1<<20)
	old.lease = first.LeaseID
	if _, err := n.st.ReapLeases(ctx, time.Now().Add(2*time.Minute), 5); err != nil {
		t.Fatal(err)
	}
	second, ok, err := n.st.Claim(ctx, "agent:bot", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	cur := c.begin("s-new", m.ID, 1<<20, 1, 1<<20)
	cur.lease = second.LeaseID
	c.record(old, "text", map[string]string{"text": "late"})
	c.record(cur, "text", map[string]string{"text": "now"})
	evs, err := n.st.Events(ctx, m.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Data != `{"text":"now"}` {
		t.Fatalf("events %v: only the current attempt's", evs)
	}
}
