package node

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A turn of 100 tokens without a cost is published by a cancel; a cost line then goes out as its
// delta (no tokens, $0.25). A receiving node with a price for the model counts one turn of 100
// tokens that cost 0.25, nothing uncosted, nothing estimated from its prices.
func TestReceiverFoldsADeltaIntoItsTurn(t *testing.T) {
	src, err := store.Open("memory:", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	m := store.Message{ID: store.NewID(), From: "user:a", To: "agent:x", Body: "hi"}
	if _, _, err := src.Insert(ctx, &m); err != nil {
		t.Fatal(err)
	}
	c, ok, err := src.Claim(ctx, "agent:x", time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	reading := func(cost *float64) string {
		b, _ := json.Marshal(store.Usage{Turn: "sbu_t1", Agent: "agent:x@src", Model: "m", Reported: true, Input: 100, Total: 100, Cost: cost, Currency: "USD"})
		return string(b)
	}
	if err := src.PutUsage(ctx, c.ID, c.LeaseID, reading(nil), false); err != nil {
		t.Fatal(err)
	}
	if _, err := src.Cancel(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	cost := 0.25
	if err := src.PutUsage(ctx, c.ID, c.LeaseID, reading(&cost), false); err != nil {
		t.Fatal(err)
	}
	evs, err := src.Events(ctx, c.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var data []string
	for _, e := range evs {
		if e.Kind == "usage" {
			data = append(data, e.Data)
		}
	}
	if len(data) != 2 {
		t.Fatalf("usage events %q: want the turn, then its delta", data)
	}
	n := newNode(t, "prices:\n  m: {input: 3, output: 15}\n")
	rm := store.Message{ID: m.ID, From: "user:a", To: "agent:x@src", Body: "hi"}
	if _, _, err := n.st.Insert(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	for _, d := range data {
		if err := n.st.AddEvent(ctx, m.ID, "usage", d); err != nil {
			t.Fatal(err)
		}
	}
	reps, err := n.Usage(ctx, me, store.UsageQuery{})
	if err != nil || len(reps) != 1 {
		t.Fatalf("usage %+v %v", reps, err)
	}
	r := reps[0]
	if r.Turns != 1 || len(r.Uncosted) != 0 || r.Estimated != 0 || r.Cost != 0.25 || r.Input != 100 {
		t.Fatalf("the receiver: turns %d, uncosted %v, estimated %v, cost %v, input %d; want 1, none, 0, 0.25, 100",
			r.Turns, r.Uncosted, r.Estimated, r.Cost, r.Input)
	}
}
