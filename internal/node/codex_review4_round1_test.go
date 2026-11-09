package node

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// The first attempt reads 100 tokens of the turn and breaks off (retried); the message is cancelled
// while it waits for its next attempt: the usage it counted goes among its events all the same, so
// the sender's node gets it, not only this node's count.
func TestCancelBetweenAttemptsPublishesUsage(t *testing.T) {
	n, _ := sandboxNodeOn(t, breakingSandbox(t, false), "store: \"memory:\"\nlimits: {max_attempts: 3}\n")
	m, _, err := n.Send(ctx, me, SendRequest{To: "cloud", Body: "count me"})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(2 * time.Millisecond) {
		got, err := n.st.Get(ctx, m.ID)
		if err == nil && got.State == store.Queued && got.Attempts == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never queued again after its first attempt: %s attempts=%d", got.State, got.Attempts)
		}
	}
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	evs, err := n.st.Events(ctx, m.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var inputs []int
	for _, e := range evs {
		if e.Kind == "usage" {
			var u store.Usage
			if jsonErr := json.Unmarshal([]byte(e.Data), &u); jsonErr != nil {
				t.Fatal(jsonErr)
			}
			inputs = append(inputs, u.Input)
		}
	}
	if len(inputs) != 1 || inputs[0] != 100 {
		t.Fatalf("usage events of the cancelled message: %v (want one of 100 input tokens)", inputs)
	}
}
