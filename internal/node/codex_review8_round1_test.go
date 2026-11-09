package node

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// The sandbox says a turn's tokens, then its cost on a line of its own (the tokens unchanged): the
// message's usage event and this node's count carry the cost.
func TestSandboxCostLineOfItsOwnIsKept(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	f.frames = []string{
		"USER:6",
		frame(7, "r1", "log", `{"type":"usage","model":"claude-x","input_tokens":100}`, nil),
		frame(8, "r1", "log", `{"type":"usage","cost_usd":0.25}`, nil),
		frame(9, "r1", "assistant", "Done.", nil),
		frame(10, "r1", "log", `{"type":"acp.turn_end","stop_reason":"end_turn"}`, nil),
	}
	n, _ := sandboxNodeOn(t, f.handler, "store: \"memory:\"\n")
	m, _, err := n.Send(ctx, me, SendRequest{To: "cloud", Body: "build it"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Done {
		t.Fatalf("%s %s %q %v", w.State, w.ErrorCode, w.Error, err)
	}
	evs, err := n.st.Events(ctx, m.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var got []store.Usage
	for _, e := range evs {
		if e.Kind == "usage" {
			var u store.Usage
			if err := json.Unmarshal([]byte(e.Data), &u); err != nil {
				t.Fatal(err)
			}
			got = append(got, u)
		}
	}
	sums, err := n.st.UsageOf(ctx, store.UsageQuery{})
	if err != nil || len(got) != 1 || got[0].Cost == nil || *got[0].Cost != 0.25 || got[0].Input != 100 || len(sums) != 1 || sums[0].Cost != 0.25 {
		t.Fatalf("usage events %+v, counted %+v %v: want 100 tokens and the cost 0.25 in both", got, sums, err)
	}
}
