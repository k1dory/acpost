package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

func TestWaitFinalTimeoutBoundsHTTPRequest(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(250 * time.Millisecond)
		_ = json.NewEncoder(w).Encode(store.Message{
			ID: "msg_test", State: store.Queued,
		})
	}))
	defer h.Close()

	c, err := New(h.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	start := time.Now()
	_, _ = c.WaitFinal(ctx, "msg_test", 10*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("10ms timeout took %v", elapsed)
	}
}
