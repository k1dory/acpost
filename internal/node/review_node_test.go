package node

// Regression tests from the Codex review (gpt-6-astra, 2026-11-05).

import (
	"encoding/json"
	"errors"
	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestHubDeliveryCannotAdoptLocalMessage(t *testing.T) {
	n := newNode(t, `
node: {name: edge}
agents:
  public: {driver: inbox}
  private: {driver: inbox, allow_from: ["user:tester"]}
`)
	m, _, err := n.Send(ctx, me, SendRequest{To: "private", Body: "private task"})
	if err != nil {
		t.Fatal(err)
	}
	c, ok, err := n.Claim(ctx, me, "private", 0, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if err := n.Reply(ctx, me, c.ID, c.LeaseID, "private reply", ""); err != nil {
		t.Fatal(err)
	}

	replies := make(chan client.ReplyRequest, 1)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var reply client.ReplyRequest
		if err := json.NewDecoder(r.Body).Decode(&reply); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		replies <- reply
		w.WriteHeader(204)
	}))
	defer h.Close()
	n.hub, err = client.New(h.URL, "dummy", "")
	if err != nil {
		t.Fatal(err)
	}

	n.deliver(store.Message{
		ID: m.ID, From: "agent:attacker@peer",
		To: "agent:public@edge", Body: "unrelated task", LeaseID: "hub-lease",
	})

	select {
	case reply := <-replies:
		if reply.Body == "private reply" {
			t.Fatal("relayed a private local reply to the hub")
		}
	case <-time.After(time.Second):
	}
	got, err := n.st.Get(ctx, m.ID)
	if err != nil || got.Origin != "" {
		t.Fatalf("local message adopted by hub: origin=%q err=%v", got.Origin, err)
	}
}

func TestHubClaimQualifiesHubLocalSender(t *testing.T) {
	hub := newNode(t, "node: {name: hub}\n")
	edge := Principal{Kind: "node", Name: "edge"}
	if err := hub.Announce(ctx, edge, "edge", []string{"bot"}); err != nil {
		t.Fatal(err)
	}
	_, _, err := hub.Send(ctx, Principal{Kind: "user", Name: "boss"},
		SendRequest{To: "agent:bot@edge", Body: "run"})
	if err != nil {
		t.Fatal(err)
	}

	m, ok, err := hub.NodeClaim(ctx, edge, "edge", 0, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	if m.From != "user:boss@hub" {
		t.Fatalf("sender crosses node boundary as %q", m.From)
	}
}

func TestRemoteCancelPreservesHubConflict(t *testing.T) {
	n := newNode(t, inboxCfg)
	m, _, err := n.st.Insert(ctx, &store.Message{
		ID: store.NewID(), From: me.Address(), To: "agent:r@peer",
		Body: "work", Origin: originRemote,
	})
	if err != nil {
		t.Fatal(err)
	}

	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": &Error{Code: "conflict", Message: "message is already done"},
		})
	}))
	defer h.Close()
	n.hub, err = client.New(h.URL, "dummy", "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := n.Cancel(ctx, me, m.ID); code(err) != "conflict" {
		t.Fatalf("hub's final-state conflict was ignored: %v", err)
	}
	got, _ := n.st.Get(ctx, m.ID)
	if got.State == store.Cancelled {
		t.Fatal("overwrote the completed remote outcome")
	}
}

func TestHubDeliveryEnforcesReceivingLimits(t *testing.T) {
	tests := []struct {
		name, body    string
		meta          map[string]string
		depth, queued int
	}{
		{"body", strings.Repeat("x", 1025), nil, 0, 0},
		{"meta", "x", map[string]string{"k": strings.Repeat("v", 65)}, 0, 0},
		{"depth", "x", nil, 3, 0},
		{"queue", "x", nil, 0, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			n := newNode(t, inboxCfg)
			for range tc.queued {
				if _, _, err := n.Send(ctx, me,
					SendRequest{To: "bot", Body: "existing"}); err != nil {
					t.Fatal(err)
				}
			}
			h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(204)
			}))
			defer h.Close()
			var err error
			n.hub, err = client.New(h.URL, "dummy", "")
			if err != nil {
				t.Fatal(err)
			}

			id := store.NewID()
			n.deliver(store.Message{
				ID: id, From: "user:remote@peer",
				To: "agent:bot@" + n.Name(), Body: tc.body,
				Meta: tc.meta, Depth: tc.depth, LeaseID: "dummy-lease",
			})
			if _, err := n.st.Get(ctx, id); !errors.Is(err, store.ErrNotFound) {
				t.Fatalf("accepted hub delivery exceeding %s limit: %v", tc.name, err)
			}
		})
	}
}

func TestQueueAdmissionAndIdempotency(t *testing.T) {
	const cfg = `
limits: {max_queued_per_agent: 1}
agents: {bot: {driver: inbox}}
`
	t.Run("idempotent retry at capacity", func(t *testing.T) {
		n := newNode(t, cfg)
		req := SendRequest{To: "bot", Body: "x", IdemKey: "k"}
		first, _, err := n.Send(ctx, me, req)
		if err != nil {
			t.Fatal(err)
		}
		again, created, err := n.Send(ctx, me, req)
		if err != nil || created || again.ID != first.ID {
			t.Fatalf("retry at capacity: id=%q created=%v err=%v",
				again.ID, created, err)
		}
	})

	t.Run("concurrent admission", func(t *testing.T) {
		n := newNode(t, cfg)
		start := make(chan struct{})
		var wg sync.WaitGroup
		for range 64 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				_, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"})
				if err != nil && code(err) != "refused" {
					t.Error(err)
				}
			}()
		}
		close(start)
		wg.Wait()
		queued, _, err := n.st.Counts(ctx, "agent:bot")
		if err != nil || queued > 1 {
			t.Fatalf("queue limit 1 admitted %d messages: %v", queued, err)
		}
	})
}

func TestSmallReplyLimitDoesNotPanic(t *testing.T) {
	n := newNode(t, `
limits: {max_reply: 8}
agents: {bot: {driver: inbox}}
`)
	m, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"})
	if err != nil {
		t.Fatal(err)
	}
	c, ok, err := n.Claim(ctx, me, "bot", 0, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	defer func() {
		if p := recover(); p != nil {
			t.Fatalf("small reply limit panicked: %v", p)
		}
	}()
	if err := n.Reply(ctx, me, c.ID, c.LeaseID, "123456789", ""); err != nil {
		t.Fatal(err)
	}
	got, err := n.Get(ctx, me, m.ID)
	if err != nil || len(got.Reply) > 8 {
		t.Fatalf("reply length=%d err=%v", len(got.Reply), err)
	}
}
