package node

import (
	"context"
	"errors"
	"testing"
	"time"
)

// A request cut short (the node stops: the API ends every request's context) keeps its cause, so
// the API answers unavailable and the client asks again: an approval in wait records no decision,
// and a message that is there is not "not found".
func TestCutRequestsKeepTheirCause(t *testing.T) {
	n := newNode(t, inboxCfg)
	m, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "ship it"})
	if err != nil {
		t.Fatal(err)
	}
	tok := n.issueAgentToken("bot")
	bot := Principal{Kind: "agent", Name: "bot", agent: tok}
	n.setCurrent(tok, m.ID, "")

	cctx, cancel := context.WithCancel(ctx)
	go func() {
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if reqs, _ := n.Asking(me); len(reqs) == 1 {
				cancel() // the stop, while the person has not answered
				return
			}
		}
		t.Error("nothing asked")
		cancel()
	}()
	if err := n.approve(cctx, bot, "merge pull request 7", "", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("approval cut by the stop: %v", err)
	}
	evs, _ := n.Events(ctx, me, m.ID, 0, 100)
	for _, e := range evs {
		if e.Kind == "permission" {
			t.Fatalf("a decision recorded for an approval nobody gave: %s", e.Data)
		}
	}

	gone, stop := context.WithCancel(ctx)
	stop()
	if err := n.ReplyCoded(gone, me, m.ID, "lease", "x", "", "", nil); !errors.Is(err, context.Canceled) {
		t.Errorf("reply cut short: %v", err)
	}
	if err := n.Extend(gone, me, m.ID, "lease", time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("extend cut short: %v", err)
	}
	peer := Principal{Kind: "node", Name: "peer"}
	if _, err := n.RelayEvents(gone, peer, m.ID, "lease", 0, nil); !errors.Is(err, context.Canceled) {
		t.Errorf("relay cut short: %v", err)
	}
	// A message that is not there is still not found.
	if err := n.ReplyCoded(ctx, me, "msg_none", "lease", "x", "", "", nil); code(err) != "not_found" {
		t.Errorf("no such message: %v", err)
	}
}
