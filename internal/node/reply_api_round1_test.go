package node_test

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/api"
	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
)

// Over HTTP, as an inbox consumer retries after a network error: the same reply with its files
// sent again is a success, with the reply's files at max_files and one copy of each.
func TestReplyRetriedOverHTTP(t *testing.T) {
	ctx := context.Background()
	cfg, err := config.Parse([]byte("limits: {max_files: 2}\nagents:\n  bot: {driver: inbox}\n"), ".yaml")
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
	if err := n.Start(ctx); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(api.Handler(n, true, 64<<20))
	t.Cleanup(func() { srv.Close(); n.Close(); st.Close() })
	c, err := client.New(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := c.Send(ctx, client.SendRequest{To: "bot", Body: "review"}, "")
	if err != nil {
		t.Fatal(err)
	}
	got, ok, err := c.Claim(ctx, "bot", time.Second, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	req := client.ReplyRequest{Lease: got.LeaseID, Body: "looks fine", Files: store.Files{
		{Name: "a.md", Data: []byte("a")}, {Name: "b.md", Data: []byte("b")}}}
	for i := range 3 {
		if err := c.Reply(ctx, m.ID, req); err != nil {
			t.Fatalf("reply %d: %v", i, err)
		}
	}
	files, err := c.Files(ctx, m.ID)
	if err != nil || len(files) != 2 {
		t.Fatalf("%+v %v", files, err)
	}
}
