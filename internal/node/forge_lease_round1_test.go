package node

import (
	"context"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A forge approval asked from an attempt whose lease is no longer the message's (reaped, taken
// again) is not put among the message's events: the attempt that runs now is not asked for it.
func TestForgeApprovalOfALostAttemptIsNotRecorded(t *testing.T) {
	n := roundNode(t, nil, "")
	m := store.Message{ID: store.NewID(), From: me.Address(), To: "agent:ghost", Body: "work"}
	if _, _, err := n.st.Insert(ctx, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := n.st.Claim(ctx, "agent:ghost", time.Minute); err != nil || !ok {
		t.Fatal(ok, err)
	}
	token := n.issueAgentToken("fake")
	defer n.revokeAgentToken(token)
	n.setCurrent(token, m.ID, "lease-of-an-earlier-attempt")
	p, ok := n.PrincipalForToken(token)
	if !ok {
		t.Fatal("no principal for the agent's token")
	}
	actx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	if err := n.approve(actx, p, "merge the pull request", "x", nil); err == nil {
		t.Fatal("approved by nobody")
	}
	evs, err := n.st.Events(ctx, m.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind == "permission_request" || e.Kind == "permission" {
			t.Fatalf("the lost attempt's %s was recorded: %s", e.Kind, e.Data)
		}
	}
}
