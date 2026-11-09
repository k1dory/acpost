package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/forge"
)

// An answer whose body does not come after its headers does not hold the call: the drain of an
// unwanted body is bounded in time as in size.
func TestDrainIsBoundedInTime(t *testing.T) {
	setVar(t, &drainTimeout, 200*time.Millisecond)
	release := make(chan struct{})
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select { // the body never comes
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
	case err := <-res:
		if err != nil || time.Since(start) > 2*time.Second {
			t.Fatalf("after %v: %v", time.Since(start), err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a body that does not come holds the call")
	}
}
