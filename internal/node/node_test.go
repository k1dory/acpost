package node

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

var ctx = context.Background()

var me = Principal{Kind: "user", Name: "tester"}

func newNode(t testing.TB, yaml string) *Node {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Store = "memory:"
	st, err := store.Open(cfg.Store, StoreOptions(cfg))
	if err != nil {
		t.Fatal(err)
	}
	n, err := New(cfg, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.Close(); st.Close() })
	return n
}

// fileNode is a node on the store its configuration names.
func fileNode(t testing.TB, yaml string) (*Node, *store.Store) {
	t.Helper()
	cfg, err := config.Parse([]byte(yaml), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(cfg.Store, StoreOptions(cfg))
	if err != nil {
		t.Fatal(err)
	}
	n, err := New(cfg, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { n.Close(); st.Close() })
	return n, st
}

func code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if err != nil {
		return "other:" + err.Error()
	}
	return ""
}

const inboxCfg = `
limits: {max_body: 1KiB, max_meta: 64, max_depth: 2, max_queued_per_agent: 3}
agents:
  bot: {driver: inbox, lease: 1m}
  picky: {driver: inbox, allow_from: ["user:boss"]}
`

func TestParseAddress(t *testing.T) {
	for in, want := range map[string]string{
		"reviewer": "agent:reviewer", "agent:reviewer": "agent:reviewer", "agent:r@laptop": "agent:r@laptop",
		"user:lev": "user:lev",
	} {
		a, err := ParseAddress(in)
		if err != nil || a.String() != want {
			t.Fatalf("%s → %v %v", in, a, err)
		}
	}
	for _, bad := range []string{"", "agent:", "agent:a b", "robot:x", "agent:x@", "agent:../x"} {
		if _, err := ParseAddress(bad); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestSendInboxClaimReplyWait(t *testing.T) {
	n := newNode(t, inboxCfg)
	m, created, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "hello", Task: "T-1"})
	if err != nil || !created {
		t.Fatal(err)
	}
	if m.From != "user:tester" || m.To != "agent:bot" || m.State != store.Queued || m.Depth != 0 {
		t.Fatalf("%+v", m)
	}
	var waited store.Message
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		waited, err = n.Wait(ctx, me, m.ID, 5*time.Second)
	}()
	c, ok, err := n.Claim(ctx, me, "bot", time.Second, time.Minute)
	if err != nil || !ok || c.ID != m.ID {
		t.Fatal(ok, err)
	}
	if err := n.Reply(ctx, me, c.ID, c.LeaseID, "hi back", ""); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err != nil || waited.State != store.Done || waited.Reply != "hi back" {
		t.Fatalf("waited %+v %v", waited, err)
	}
}

func TestClaimWaitsForArrival(t *testing.T) {
	n := newNode(t, inboxCfg)
	got := make(chan store.Message, 1)
	go func() {
		c, ok, _ := n.Claim(ctx, me, "bot", 5*time.Second, time.Minute)
		if ok {
			got <- c
		}
	}()
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	m, _, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"})
	select {
	case c := <-got:
		if c.ID != m.ID {
			t.Fatal(c.ID)
		}
		if time.Since(start) > time.Second {
			t.Fatal("claim was not woken by the send")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("claim never returned")
	}
	if _, ok, _ := n.Claim(ctx, me, "bot", 10*time.Millisecond, time.Minute); ok {
		t.Fatal("claimed from empty")
	}
}

func TestWaitTimeoutReturnsCurrentState(t *testing.T) {
	n := newNode(t, inboxCfg)
	m, _, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"})
	start := time.Now()
	w, err := n.Wait(ctx, me, m.ID, 100*time.Millisecond)
	if err != nil || w.State != store.Queued {
		t.Fatal(w.State, err)
	}
	if d := time.Since(start); d < 90*time.Millisecond || d > time.Second {
		t.Fatalf("waited %v", d)
	}
}

func TestCancelWakesWaiter(t *testing.T) {
	n := newNode(t, inboxCfg)
	m, _, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"})
	done := make(chan store.Message)
	go func() { w, _ := n.Wait(ctx, me, m.ID, 10*time.Second); done <- w }()
	time.Sleep(20 * time.Millisecond)
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case w := <-done:
		if w.State != store.Cancelled {
			t.Fatal(w.State)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("waiter not woken")
	}
	if _, err := n.Cancel(ctx, me, m.ID); code(err) != "conflict" {
		t.Fatalf("cancel final: %v", err)
	}
}

func TestSendRefusals(t *testing.T) {
	n := newNode(t, inboxCfg)
	for name, tc := range map[string]struct {
		p    Principal
		r    SendRequest
		code string
	}{
		"unknown agent": {me, SendRequest{To: "ghost", Body: "x"}, "not_found"},
		"bad address":   {me, SendRequest{To: "a b", Body: "x"}, "invalid"},
		"to a user":     {me, SendRequest{To: "user:lev", Body: "x"}, "invalid"},
		"empty body":    {me, SendRequest{To: "bot"}, "invalid"},
		"body too big":  {me, SendRequest{To: "bot", Body: strings.Repeat("x", 1025)}, "refused"},
		"meta too big":  {me, SendRequest{To: "bot", Body: "x", Meta: map[string]string{"k": strings.Repeat("v", 100)}}, "refused"},
		"not allowed":   {me, SendRequest{To: "picky", Body: "x"}, "refused"},
		"negative ttl":  {me, SendRequest{To: "bot", Body: "x", TTL: -time.Second}, "invalid"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := n.Send(ctx, tc.p, tc.r); code(err) != tc.code {
				t.Fatalf("got %v, want %s", err, tc.code)
			}
		})
	}
	if _, _, err := n.Send(ctx, Principal{Kind: "user", Name: "boss"}, SendRequest{To: "picky", Body: "x"}); err != nil {
		t.Fatalf("allowed sender refused: %v", err)
	}
}

func TestQueueLimit(t *testing.T) {
	n := newNode(t, inboxCfg)
	for range 3 {
		if _, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"}); code(err) != "refused" {
		t.Fatalf("4th queued: %v", err)
	}
}

func TestIdempotentSend(t *testing.T) {
	n := newNode(t, inboxCfg)
	a, c1, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "x", IdemKey: "k"})
	b, c2, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "x", IdemKey: "k"})
	if !c1 || c2 || a.ID != b.ID {
		t.Fatal(c1, c2, a.ID, b.ID)
	}
}

// A message sent by an agent process (its token) is from that agent, child of the message it is
// working on, one level deeper; past max_depth it is refused.
func TestAgentTokenChain(t *testing.T) {
	n := newNode(t, inboxCfg+"  helper: {driver: inbox}\n")
	root, _, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "start", Task: "T"})
	tok := n.issueAgentToken("bot")
	n.setCurrent(tok, root.ID)
	p, ok := n.PrincipalForToken(tok)
	if !ok || p.Address() != "agent:bot" {
		t.Fatal(p, ok)
	}
	child, _, err := n.Send(ctx, p, SendRequest{To: "helper", Body: "sub"})
	if err != nil {
		t.Fatal(err)
	}
	if child.From != "agent:bot" || child.Parent != root.ID || child.Depth != 1 || child.Task != "T" {
		t.Fatalf("child %+v", child)
	}
	tok2 := n.issueAgentToken("helper")
	n.setCurrent(tok2, child.ID)
	p2, _ := n.PrincipalForToken(tok2)
	grand, _, err := n.Send(ctx, p2, SendRequest{To: "bot", Body: "subsub"})
	if err != nil || grand.Depth != 2 {
		t.Fatal(grand.Depth, err)
	}
	tok3 := n.issueAgentToken("bot")
	n.setCurrent(tok3, grand.ID)
	p3, _ := n.PrincipalForToken(tok3)
	if _, _, err := n.Send(ctx, p3, SendRequest{To: "helper", Body: "too deep"}); code(err) != "refused" {
		t.Fatalf("depth 3 with max 2: %v", err)
	}
	if _, ok := n.PrincipalForToken("forged"); ok {
		t.Fatal("unknown token accepted")
	}
}

func TestClientTokenPermissions(t *testing.T) {
	n := newNode(t, inboxCfg+`
tokens:
  - {name: ci, sha256: 2bb80d537b1da3e38bd30361aa855686bde0eacd7162fef6a25fe97bf527a25b, send_to: ["agent:bot"]}
  - {name: worker, sha256: 87eba76e7f3164534045ba922e7770fb58bbd14ad732bbf5ba6f11cc56989e6e, act_as: ["bot"]}
`) // sha256("secret"), sha256("worker")
	ci, ok := n.PrincipalForToken("secret")
	if !ok || ci.Address() != "client:ci" {
		t.Fatal(ci, ok)
	}
	m, _, err := n.Send(ctx, ci, SendRequest{To: "bot", Body: "x"})
	if err != nil || m.From != "client:ci" {
		t.Fatal(err)
	}
	if _, _, err := n.Send(ctx, ci, SendRequest{To: "picky", Body: "x"}); code(err) != "refused" {
		t.Fatalf("send outside send_to: %v", err)
	}
	if _, _, err := n.Claim(ctx, ci, "bot", 0, time.Minute); code(err) != "refused" {
		t.Fatalf("claim without act_as: %v", err)
	}
	w, _ := n.PrincipalForToken("worker")
	c, ok, err := n.Claim(ctx, w, "bot", 0, time.Minute)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if err := n.Reply(ctx, ci, c.ID, c.LeaseID, "x", ""); code(err) != "refused" {
		t.Fatalf("reply by non-owner: %v", err)
	}
	// A client sees what it sent and what it acts for, nothing else.
	other, _, _ := n.Send(ctx, me, SendRequest{To: "picky", Body: "private"})
	other2, _, _ := n.Send(ctx, Principal{Kind: "user", Name: "boss"}, SendRequest{To: "picky", Body: "private"})
	_ = other
	if _, err := n.Get(ctx, ci, other2.ID); code(err) != "not_found" {
		t.Fatalf("client read another's message: %v", err)
	}
	if _, err := n.Get(ctx, ci, m.ID); err != nil {
		t.Fatal(err)
	}
	page, _, _ := n.List(ctx, ci, store.Filter{Limit: 100})
	for _, x := range page {
		if x.From != "client:ci" && x.To != "agent:bot" {
			t.Fatalf("client listed %+v", x)
		}
	}
}

func TestTTLExpiresQueued(t *testing.T) {
	n := newNode(t, inboxCfg)
	n.setMaintainEvery(20 * time.Millisecond)
	m, _, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "x", TTL: 30 * time.Millisecond})
	w, _ := n.Wait(ctx, me, m.ID, 3*time.Second)
	if w.State != store.Expired {
		t.Fatal(w.State)
	}
}

func TestAgentsListing(t *testing.T) {
	n := newNode(t, inboxCfg)
	n.Send(ctx, me, SendRequest{To: "bot", Body: "x"})
	agents := n.Agents(ctx)
	if len(agents) != 2 || agents[0].Name != "bot" || agents[0].Queued != 1 || agents[0].Driver != "inbox" {
		t.Fatalf("%+v", agents)
	}
}
