package node

import (
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// sandboxNodeOn is a node with a sandbox agent on a sandboxd that h serves (extra: lines of the
// config before agents).
func sandboxNodeOn(t *testing.T, h http.HandlerFunc, head string) (*Node, *store.Store) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return sandboxNodeAt(t, srv.URL, head)
}

// sandboxNodeAt is sandboxNodeOn on a sandboxd already serving at url.
func sandboxNodeAt(t *testing.T, url, head string) (*Node, *store.Store) {
	t.Helper()
	t.Setenv("SB_TOKEN", "sbtoken")
	return fileNode(t, head+`
agents:
  cloud:
    driver: sandbox
    turn_timeout: 30s
    sandbox: {url: "`+url+`", id: sb1, token_env: SB_TOKEN}
`)
}

// A6/X10: sandboxd took the message (201) but its answer broke off: the message is posted again
// under the same Idempotency-Key and its turn followed, not failed while the run goes on there.
func TestSandboxBrokenAnswerIsRetried(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	f.frames = []string{
		"USER:6",
		frame(7, "r1", "assistant", "Done it.", nil),
		frame(8, "r1", "log", `{"type":"acp.turn_end","stop_reason":"end_turn"}`, nil),
	}
	var once sync.Once
	h := func(w http.ResponseWriter, r *http.Request) {
		broke := false
		if r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/messages") {
			once.Do(func() {
				broke = true
				f.handler(httptest.NewRecorder(), r) // sandboxd takes it
				conn, buf, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				buf.WriteString("HTTP/1.1 201 Created\r\nContent-Type: application/json\r\nContent-Length: 200\r\n\r\n{\"run\":{\"id\":\"r1\",")
				buf.Flush()
				conn.Close()
			})
		}
		if !broke {
			f.handler(w, r)
		}
	}
	n, _ := sandboxNodeOn(t, h, "store: \"memory:\"\n")
	m, _, err := n.Send(ctx, me, SendRequest{To: "cloud", Body: "build it"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Done || w.Reply != "Done it." {
		t.Fatalf("%s reply=%q %s %q attempts=%d", w.State, w.Reply, w.ErrorCode, w.Error, w.Attempts)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.keys) != 2 || f.keys[0] != f.keys[1] {
		t.Fatalf("posted with keys %q: twice, under one key", f.keys)
	}
}

// X14: a turn's usage read by two attempts (the first lost its lease after reading it) is one
// turn's usage: the second is not counted again.
func TestSandboxUsageCountedOnce(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	n := sandboxNode(t, f)
	n.sbMu.Lock()
	a := n.sandboxes["cloud"]
	n.sbMu.Unlock()
	msg, _, err := n.Send(ctx, me, SendRequest{To: "cloud", Body: "count me"})
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		tr := &sandboxTurn{a: a, msgID: msg.ID, run: sandboxRun{ID: "r7", Session: "s-1"}}
		tr.used, tr.usage = true, store.Usage{Input: 100, Output: 20}
		a.recordUsage(msg.ID, "", tr)
	}
	sums, err := n.st.UsageOf(ctx, store.UsageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Turns != 1 || sums[0].Input != 100 {
		t.Fatalf("usage %+v: one turn of 100 input tokens", sums)
	}
}

// A7: the cursor in the sandbox's conversation outlives the node: after a restart the first read
// of the history starts where the last turn left it, not from the start.
func TestSandboxCursorOutlivesARestart(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	f.frames = []string{
		"USER:6",
		frame(7, "r1", "assistant", "first", nil),
		frame(8, "r1", "log", `{"type":"acp.turn_end","stop_reason":"end_turn"}`, nil),
	}
	var mu sync.Mutex
	var afters []string
	h := func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/messages") {
			mu.Lock()
			afters = append(afters, r.URL.Query().Get("after"))
			mu.Unlock()
		}
		f.handler(w, r)
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	head := "store: sqlite://" + dir + "/store.db\n"
	srv := httptest.NewServer(http.HandlerFunc(h))
	t.Cleanup(srv.Close)
	n1, st1 := sandboxNodeAt(t, srv.URL, head)
	m, _, err := n1.Send(ctx, me, SendRequest{To: "cloud", Body: "one"})
	if err != nil {
		t.Fatal(err)
	}
	if w, _ := n1.Wait(ctx, me, m.ID, 20*time.Second); w.State != store.Done {
		t.Fatalf("first: %s %q", w.State, w.Error)
	}
	time.Sleep(50 * time.Millisecond) // the loop keeps the cursor after the turn
	n1.Close()
	st1.Close()

	mu.Lock()
	afters = nil
	mu.Unlock()
	f.mu.Lock()
	f.frames = append(f.frames, "USER:9", frame(10, "r1", "assistant", "second", nil), frame(11, "r1", "log", `{"type":"acp.turn_end","stop_reason":"end_turn"}`, nil))
	f.mu.Unlock()
	n2, _ := sandboxNodeAt(t, srv.URL, head)
	m2, _, err := n2.Send(ctx, me, SendRequest{To: "cloud", Body: "two"})
	if err != nil {
		t.Fatal(err)
	}
	if w, _ := n2.Wait(ctx, me, m2.ID, 20*time.Second); w.State != store.Done {
		t.Fatalf("second: %s %q", w.State, w.Error)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(afters) == 0 || afters[0] != "8" {
		t.Fatalf("history read after %v after the restart: from the cursor kept (8)", afters)
	}
}
