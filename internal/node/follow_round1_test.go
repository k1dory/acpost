package node

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/k1dory/acpost/internal/client"
)

// A forward finishing while the node closes starts no follow loop on the closed node.
func TestStartFollowAfterCloseStartsNothing(t *testing.T) {
	n := newNode(t, inboxCfg)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer h.Close()
	n.hub, _ = client.New(h.URL, "dummy", "")
	n.Close()
	n.startFollow("msg_x")
	n.followMu.Lock()
	following := len(n.following)
	n.followMu.Unlock()
	if following != 0 {
		t.Fatalf("a follow loop started after Close (%d following)", following)
	}
}
