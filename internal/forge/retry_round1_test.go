package forge

import (
	"net/http"
	"sync/atomic"
	"testing"
)

// A rate-limited request (429, or 403 with Retry-After) is asked again once after a short wait it
// names; a long one is not waited out.
func TestRetryAfterOnce(t *testing.T) {
	for _, status := range []int{429, 403} {
		var n atomic.Int32
		c, host, _ := fake(t, GitHub, map[string]any{"GET /api/v3/user": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if n.Add(1) == 1 {
				w.Header().Set("Retry-After", "0")
				w.WriteHeader(status)
				return
			}
			w.Write([]byte(`{"login":"lev"}`))
		})})
		srv := Server{Kind: GitHub, API: "https://" + host + "/api/v3"}
		if login, err := c.Whoami(ctx, srv, host); err != nil || login != "lev" || n.Load() != 2 {
			t.Fatalf("%d: %q %v after %d requests", status, login, err, n.Load())
		}
	}
	var n atomic.Int32
	c, host, _ := fake(t, GitHub, map[string]any{"GET /api/v3/user": http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	})})
	if _, err := c.Whoami(ctx, Server{Kind: GitHub, API: "https://" + host + "/api/v3"}, host); err == nil || n.Load() != 1 {
		t.Fatalf("an hour's wait: %v after %d requests", err, n.Load())
	}
}
