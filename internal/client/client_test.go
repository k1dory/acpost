package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	// A field this version does not read is a newer node's: taken, without it (the node checks
	// what came against the listing: asListed).
	if fs, err := c.SideFiles(context.Background(), "odd", true, 1<<20); err != nil || len(fs) != 1 || fs[0].Data != nil {
		t.Fatalf("a list with a field unknown here: %+v %v", fs, err)
	}
	if fs, err := c.SideFiles(context.Background(), "msg_x", true, 1<<20); err != nil || len(fs) != 1 {
		t.Fatalf("within the cap: %v", err)
	}
}

// A broken event stream is reopened after the last event seen; the events already read are not
// read again, and the end ends it.
func TestFollowResumes(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		switch calls.Add(1) {
		case 1:
			fmt.Fprint(w, "id: 1\nevent: text\ndata: {\"seq\":1,\"kind\":\"text\",\"data\":\"{}\"}\n\n: ping\n\nid: 2\nevent: text\ndata: {\"seq\":2,\"kind\":\"text\",\"data\":\"{}\"}\n\n")
			// and the connection drops
		default:
			if r.URL.Query().Get("after") != "2" {
				t.Errorf("reopened after %q", r.URL.Query().Get("after"))
			}
			fmt.Fprint(w, "id: 2\nevent: text\ndata: {\"seq\":2,\"kind\":\"text\",\"data\":\"{}\"}\n\nid: 3\nevent: notice\ndata: {\"seq\":3,\"kind\":\"notice\",\"data\":\"\\\"x\\\"\"}\n\nevent: end\ndata: {\"id\":\"msg_x\",\"state\":\"done\",\"reply\":\"ok\"}\n\n")
		}
	}))
	defer srv.Close()
	c, err := New(srv.URL, "", "")
	if err != nil {
		t.Fatal(err)
	}
	var seqs []int64
	m, err := c.Follow(context.Background(), "msg_x", 0, func(e store.Event) error { seqs = append(seqs, e.Seq); return nil })
	if err != nil || m.Reply != "ok" || fmt.Sprint(seqs) != "[1 2 3]" {
		t.Fatalf("%v %+v %v", seqs, m, err)
	}
}

// A line over the limit is not retried for ever: the same would come again.
func TestFollowLongLineEnds(t *testing.T) {
	defer func(v int) { maxEventLine = v }(maxEventLine)
	maxEventLine = 64
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: end\ndata: "+strings.Repeat("x", 200)+"\n\n")
	}))
	defer srv.Close()
	c, _ := New(srv.URL, "", "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.Follow(ctx, "msg_x", 0, func(store.Event) error { return nil }); !errors.Is(err, ErrBadResponse) || calls.Load() != 1 {
		t.Fatalf("%v after %d calls", err, calls.Load())
	}
}
