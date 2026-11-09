package node

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// A hub failing for a moment while a delivery downloads its files (500) does not fail the message
// on the hub for good: no reply goes back, the lease running out brings it again.
func TestDeliverTransientFileErrorLeftToLease(t *testing.T) {
	n := newNode(t, inboxCfg)
	var fileCalls atomic.Int32
	replies := make(chan client.ReplyRequest, 4)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/files"):
			fileCalls.Add(1)
			w.WriteHeader(500)
		case strings.HasSuffix(r.URL.Path, "/reply"):
			var rr client.ReplyRequest
			_ = json.NewDecoder(r.Body).Decode(&rr)
			replies <- rr
			w.WriteHeader(204)
		default:
			w.WriteHeader(204)
		}
	}))
	defer h.Close()
	var err error
	if n.hub, err = client.New(h.URL, "dummy", ""); err != nil {
		t.Fatal(err)
	}
	id := store.NewID()
	n.deliver(store.Message{ID: id, From: "user:x@other", To: "agent:bot@" + n.Name(), Body: "b", LeaseID: "L1",
		Files: store.Files{{Name: "f.bin", Size: 1, SHA256: strings.Repeat("0", 64)}}})
	if fileCalls.Load() < 2 {
		t.Fatalf("the files were asked for %d times: a passing failure is tried again", fileCalls.Load())
	}
	select {
	case rr := <-replies:
		t.Fatalf("a passing failure became the message's final reply: %q", rr.Error)
	case <-time.After(300 * time.Millisecond):
	}
	if _, err := n.st.Get(ctx, id); err == nil {
		t.Fatal("message stored locally despite failed download")
	}
}

// Files over this node's limits stay a final refusal: they will not get smaller.
func TestDeliverFilesOverLimitsRefused(t *testing.T) {
	n := newNode(t, "limits: {max_files_size: 4}\nagents:\n  bot: {driver: inbox, allow_from: [\"*\"]}\n")
	replies := make(chan client.ReplyRequest, 4)
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/reply") {
			var rr client.ReplyRequest
			_ = json.NewDecoder(r.Body).Decode(&rr)
			replies <- rr
		}
		w.WriteHeader(204)
	}))
	defer h.Close()
	n.hub, _ = client.New(h.URL, "dummy", "")
	n.deliver(store.Message{ID: store.NewID(), From: "user:x@other", To: "agent:bot@" + n.Name(), Body: "b", LeaseID: "L1",
		Files: store.Files{{Name: "f.bin", Size: 100, SHA256: strings.Repeat("0", 64)}}})
	select {
	case rr := <-replies:
		if rr.Error == "" {
			t.Fatal("refusal without error")
		}
	case <-time.After(time.Second):
		t.Fatal("files over the limits were not refused")
	}
}
