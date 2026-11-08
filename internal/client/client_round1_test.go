package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/forge"
	"github.com/k1dory/acpost/internal/store"
)

func setVar[T any](t *testing.T, p *T, v T) {
	old := *p
	*p = v
	t.Cleanup(func() { *p = old })
}

// A host that takes the connection and never answers the TLS hello is unavailable within the
// handshake bound, not a hang.
func TestTLSHandshakeStallIsUnavailable(t *testing.T) {
	setVar(t, &handshakeTimeout, 300*time.Millisecond)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var mu sync.Mutex
	var held []net.Conn
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			held = append(held, c) // silent: no ServerHello
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, c := range held {
			c.Close()
		}
	})
	c, err := New("https://"+ln.Addr().String(), "", "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	_, err = c.Status(ctx)
	if !errors.Is(err, ErrUnavailable) || time.Since(start) > 3*time.Second {
		t.Fatalf("after %v: %v", time.Since(start), err)
	}
}

// A node that takes the request and never answers its health check is unavailable, even for a
// caller without a deadline of its own.
func TestStatusIsBounded(t *testing.T) {
	setVar(t, &statusTimeout, 300*time.Millisecond)
	release := make(chan struct{})
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer h.Close()
	defer close(release)
	c, _ := New(h.URL, "", "")
	res := make(chan error, 1)
	go func() { _, err := c.Status(context.Background()); res <- err }()
	select {
	case err := <-res:
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("%v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Status without a deadline hangs on a node that does not answer")
	}
}

func sseServer(t *testing.T, hits *atomic.Int32, stream string) *httptest.Server {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		fmt.Fprint(w, stream)
	}))
	t.Cleanup(h.Close)
	return h
}

// An `event: error` ends the stream with the node's error: no reconnecting to a stream that
// fails the same way every time. An unavailable node is worth another try.
func TestStreamErrorEvents(t *testing.T) {
	setVar(t, &followRetry, 20*time.Millisecond)
	var hits atomic.Int32
	h := sseServer(t, &hits, "id: 1\nevent: status\ndata: {\"seq\":1,\"kind\":\"status\"}\n\nevent: error\ndata: {\"status\":403,\"error\":{\"code\":\"refused\",\"message\":\"no longer yours\"}}\n\n")
	c, _ := New(h.URL, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	seen := 0
	_, err := c.Follow(ctx, "msg_x", 0, func(store.Event) error { seen++; return nil })
	var e *Error
	if !errors.As(err, &e) || e.Code != "refused" || e.Status != 403 || errors.Is(err, ErrUnavailable) || hits.Load() != 1 || seen != 1 {
		t.Fatalf("follow: %v (%d connections, %d events)", err, hits.Load(), seen)
	}

	_, err = c.TermOutput(ctx, "term_x", 0, func(int64, []byte) error { return nil })
	if !errors.As(err, &e) || e.Code != "refused" || errors.Is(err, ErrUnavailable) {
		t.Fatalf("terminal output: %v", err)
	}

	var again atomic.Int32
	h2 := sseServer(t, &again, "event: error\ndata: {\"status\":503,\"error\":{\"code\":\"unavailable\",\"message\":\"the hub is away\"}}\n\n")
	c2, _ := New(h2.URL, "", "")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel2()
	if _, err := c2.Follow(ctx2, "msg_x", 0, func(store.Event) error { return nil }); !errors.Is(err, context.DeadlineExceeded) || again.Load() < 2 {
		t.Fatalf("unavailable: %v after %d connections", err, again.Load())
	}
}

// A stream that keeps breaking is reopened ever later, up to a ceiling.
func TestFollowBacksOff(t *testing.T) {
	setVar(t, &followRetry, 20*time.Millisecond)
	setVar(t, &followRetryMax, 80*time.Millisecond)
	var hits atomic.Int32
	h := sseServer(t, &hits, "") // the stream ends at once, nothing in it
	c, _ := New(h.URL, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 600*time.Millisecond)
	defer cancel()
	_, _ = c.Follow(ctx, "msg_x", 0, func(store.Event) error { return nil })
	// 20+40+80+80+… ms: about 9 in 600 ms; at a steady 20 ms it would be about 30.
	if n := hits.Load(); n < 3 || n > 14 {
		t.Fatalf("%d connections in 600ms", n)
	}
}

// The caller ending ctx is the caller's doing: not "node unavailable" (the app shows node_down
// for that).
func TestCanceledIsNotUnavailable(t *testing.T) {
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer h.Close()
	c, _ := New(h.URL, "", "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	checks := map[string]error{
		"TermInput": c.TermInput(ctx, "term_x", []byte("x")),
	}
	_, checks["TermOutput"] = c.TermOutput(ctx, "term_x", 0, func(int64, []byte) error { return nil })
	_, checks["CILog"] = c.CILog(ctx, ForgeQuery{Dir: "/x"}, "1", "")
	for name, err := range checks {
		if !errors.Is(err, context.Canceled) || errors.Is(err, ErrUnavailable) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

// A call that does not want the answer's body still reads it, so its connection is used again.
func TestUnreadAnswerKeepsTheConnection(t *testing.T) {
	var conns atomic.Int32
	h := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"merged":true,"number":7}`)
	}))
	h.Config.ConnState = func(_ net.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	h.Start()
	defer h.Close()
	c, _ := New(h.URL, "", "")
	for range 3 {
		if err := c.MergePull(context.Background(), ForgeQuery{Dir: "/x"}, forge.Merge{Number: 7}); err != nil {
			t.Fatal(err)
		}
	}
	if n := conns.Load(); n != 1 {
		t.Fatalf("%d connections for 3 calls", n)
	}
}

// The query of a list is the same each time: url.Values.Encode sorts its keys (the map the
// filter goes through does not make the order random).
func TestListQueryIsStable(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.RawQuery] = true
		mu.Unlock()
		fmt.Fprint(w, `{"items":[]}`)
	}))
	defer h.Close()
	c, _ := New(h.URL, "", "")
	for range 20 {
		if _, err := c.List(context.Background(), ListFilter{To: "agent:a", From: "user:b", Task: "t", Thread: "th", State: "queued", After: "m"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 1 {
		t.Fatalf("%d different queries: %v", len(seen), seen)
	}
}
