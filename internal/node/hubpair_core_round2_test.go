package node_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/acptest"
	"github.com/k1dory/acpost/internal/api"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
)

// startPairNode is a node from yaml on a store in memory, stopped when the test ends.
func startPairNode(t *testing.T, yaml string) (*node.Node, *store.Store) {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Store = "memory:"
	st, err := store.Open(cfg.Store, node.StoreOptions(cfg))
	if err != nil {
		t.Fatal(err)
	}
	n, err := node.New(cfg, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.Close(); st.Close() })
	return n, st
}

// hubPair is a hub (named hub) and a node joined to it (named edge), each with an inbox agent
// named bot.
func hubPair(t *testing.T) (hub, edge *node.Node, edgeStore *store.Store) {
	t.Helper()
	return hubPairWith(t, nil)
}

// hubPairWith is hubPair with the hub's API behind wrap (nil: as it is).
func hubPairWith(t *testing.T, wrap func(http.Handler) http.Handler) (hub, edge *node.Node, edgeStore *store.Store) {
	t.Helper()
	home := t.TempDir()
	acptest.SetHome(t.Setenv, home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	t.Setenv("ACPOST_SECRETS_FILE", "1")
	const secret = "edge-secret"
	sum := sha256.Sum256([]byte(secret))
	hub, _ = startPairNode(t, fmt.Sprintf(`node: {name: hub}
agents:
  bot: {driver: inbox}
  other: {driver: inbox}
tokens:
  - {name: edge, sha256: %s, node: true}
`, hex.EncodeToString(sum[:])))
	h := api.Handler(hub, false, 64<<20)
	if wrap != nil {
		h = wrap(h)
	}
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	t.Setenv("ACPOST_TEST_EDGE_TOKEN", secret)
	edge, edgeStore = startPairNode(t, fmt.Sprintf(`node: {name: edge, join: {url: %s, token_env: ACPOST_TEST_EDGE_TOKEN}}
agents:
  bot: {driver: inbox}
`, srv.URL))
	return hub, edge, edgeStore
}

// A message to the hub's bot from a node that has a bot of its own is the hub's bot's: the local
// bot never takes the local copy, and the sender gets the hub's bot's reply.
func TestSendToHubAgentNotTakenByLocalNamesake(t *testing.T) {
	ctx := context.Background()
	hub, edge, edgeStore := hubPair(t)
	user := edge.LocalUser()
	sent, _, err := edge.Send(ctx, user, node.SendRequest{To: "agent:bot@hub", Body: "for the hub's bot"})
	if err != nil {
		t.Fatal(err)
	}
	if sent.To != "agent:bot@hub" {
		t.Errorf("the copy is addressed %q, not the address the sender used", sent.To)
	}
	// The edge's own bot asks for work: it must not get the hub's message.
	if m, ok, err := edge.Claim(ctx, user, "bot", 300*time.Millisecond, time.Minute); err != nil || ok {
		t.Fatalf("the local bot took %s (%v): a message for agent:bot@hub", m.ID, err)
	}
	if q, _, _ := edgeStore.Counts(ctx, "agent:bot"); q != 0 {
		t.Errorf("the local bot's queue counts %d messages that are the hub's", q)
	}
	m, ok, err := hub.Claim(ctx, hub.LocalUser(), "bot", 5*time.Second, time.Minute)
	if err != nil || !ok || m.ID != sent.ID {
		t.Fatalf("the hub's bot got %+v %v %v", m.ID, ok, err)
	}
	if err := hub.Reply(ctx, hub.LocalUser(), m.ID, m.LeaseID, "from the hub's bot", ""); err != nil {
		t.Fatal(err)
	}
	got, err := edge.Wait(ctx, user, sent.ID, 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != store.Done || got.Reply != "from the hub's bot" {
		t.Fatalf("the sender got %s %q", got.State, got.Reply)
	}
	if got.To != "agent:bot@hub" {
		t.Errorf("the finished copy is addressed %q", got.To)
	}
}

// A repeat of a key addressed to another of the hub's agents is answered with the key's first
// message: its copy names the agent that message went to, by the hub's name, not the repeat's.
func TestRepeatOfKeyKeepsFirstRecipient(t *testing.T) {
	ctx := context.Background()
	hub, edge, _ := hubPair(t)
	user := edge.LocalUser()
	// The key's first send reached the hub; its copy here is gone (a store from before, a restart).
	first, _, err := hub.Send(ctx, node.Principal{Kind: "node", Name: "edge"}, node.SendRequest{To: "agent:bot", Body: "for bot",
		ID: store.NewID(), From: user.Address() + "@edge", IdemKey: "same"})
	if err != nil {
		t.Fatal(err)
	}
	got, _, err := edge.Send(ctx, user, node.SendRequest{To: "agent:other@hub", Body: "for other", IdemKey: "same"})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != first.ID || got.To != "agent:bot@hub" {
		t.Fatalf("the repeat's copy is %s to %s; the key's first is %s to agent:bot@hub", got.ID, got.To, first.ID)
	}
}
