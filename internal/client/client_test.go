package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
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

// A response over the cap is a bad response (final); one cut by the connection is unavailable (try again).
func TestSideFilesCapVersusBrokenConnection(t *testing.T) {
	big := `{"items":[{"name":"a","data":"` + strings.Repeat("A", 4096) + `"}]}`
	h := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "odd/files") {
			w.Write([]byte(`{"items":[{"name":"a","data_base64":"eA=="}]}`))
			return
		}
		if r.URL.Query().Get("cut") == "" && !strings.HasSuffix(r.URL.Path, "cut/files") {
			w.Write([]byte(big))
			return
		}
		w.Header().Set("Content-Length", "100000")
		w.Write([]byte(big[:200]))
		w.(http.Flusher).Flush()
		conn, _, _ := w.(http.Hijacker).Hijack()
		conn.Close()
	}))
	defer h.Close()
	c, err := New(h.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SideFiles(context.Background(), "msg_x", true, 1000); !errors.Is(err, ErrBadResponse) {
		t.Fatalf("over the cap: %v", err)
	}
	if _, err := c.SideFiles(context.Background(), "cut", true, 1<<20); !errors.Is(err, ErrUnavailable) || errors.Is(err, ErrBadResponse) {
		t.Fatalf("cut connection: %v", err)
	}
	if _, err := c.SideFiles(context.Background(), "odd", true, 1<<20); !errors.Is(err, ErrBadResponse) {
		t.Fatalf("a list that does not decode: %v", err)
	}
	if fs, err := c.SideFiles(context.Background(), "msg_x", true, 1<<20); err != nil || len(fs) != 1 {
		t.Fatalf("within the cap: %v", err)
	}
}
