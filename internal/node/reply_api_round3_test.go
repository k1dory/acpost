package node_test

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/api"
	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
)

// A reply with a link whose size is out of range (stored as 0, "not said") sent twice as the
// same bytes over HTTP, as a consumer retrying after a lost response does: the repeat is compared
// with the size as stored, so it is a success, not a conflict.
func TestReplyRetriedWithAnOutOfRangeLinkSize(t *testing.T) {
	for _, size := range []int64{-1, 1<<50 + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ctx := context.Background()
			cfg, err := config.Parse([]byte("agents:\n  bot: {driver: inbox}\n"), ".yaml")
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
			body := fmt.Appendf(nil, `{"lease":%q,"body":"see the report","files":[{"name":"report.txt","uri":"https://example.com/report.txt","size":%d}]}`,
				got.LeaseID, size)
			for i := range 2 {
				resp, err := http.Post(srv.URL+"/v1/messages/"+m.ID+"/reply", "application/json", bytes.NewReader(body))
				if err != nil {
					t.Fatal(err)
				}
				resp.Body.Close()
				if resp.StatusCode != http.StatusNoContent {
					t.Fatalf("reply %d: %s", i, resp.Status)
				}
			}
			files, err := c.Files(ctx, m.ID)
			if err != nil || len(files) != 1 || files[0].Size != 0 {
				t.Fatalf("%+v %v", files, err)
			}
		})
	}
}
