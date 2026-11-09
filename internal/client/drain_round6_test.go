package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/forge"
)

// A gzip answer whose header does not come: the transport's gzip reader holds its lock while it
// waits for the header, so closing the body from the timer waits too. Cancelling the request is
// what cuts the connection.
func TestDrainIsBoundedInTimeGzip(t *testing.T) {
	setVar(t, &drainTimeout, 200*time.Millisecond)
	release := make(chan struct{})
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select { // the gzip header never comes
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer h.Close()
	defer close(release)
	c, _ := New(h.URL, "", "")
	res := make(chan error, 1)
	start := time.Now()
	go func() { res <- c.MergePull(context.Background(), ForgeQuery{Dir: "/x"}, forge.Merge{Number: 7}) }()
	select {
	case <-res:
		if time.Since(start) > 2*time.Second {
			t.Fatalf("after %v", time.Since(start))
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a gzip body that does not come holds the call")
	}
}
