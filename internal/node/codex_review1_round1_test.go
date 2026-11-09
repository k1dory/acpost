package node

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// newSessionID waits for the fake agent's first session/new (FAKE_NEW_LOG) and returns its id.
func newSessionID(t *testing.T, log string) string {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(log); err == nil {
			if first, _, ok := strings.Cut(string(b), "\n"); ok && first != "" {
				return first
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("no session/new")
	return ""
}

// A task label of the node's user that spells a client's key ("T|client:ci") is not the
// client's task T: each has a session of its own.
func TestLocalTaskLabelIsNoSendersKey(t *testing.T) {
	t.Setenv("CI_TOKEN_C1", "ci-secret-token-c1")
	n := newNode(t, fmt.Sprintf(`
tokens:
  - {name: ci, token_env: CI_TOKEN_C1, send_to: ["*"]}
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1"}
    preamble: false
`, os.Args[0]))
	ci := Principal{Kind: "client", Name: "ci", Token: n.cfg.Tokens[0]}
	m, _, err := n.Send(ctx, ci, SendRequest{To: "fake", Body: "count", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	if w := waitAll(t, n, m.ID)[0]; w.Reply != "1" {
		t.Fatalf("client: %s %q %q", w.State, w.Reply, w.Error)
	}
	label := "T|" + m.From
	if k1, k2 := sessionKey("task", store.Message{From: "user:" + n.Name(), Task: label}), sessionKey("task", m); k1 == k2 {
		t.Fatalf("the user's task %q and the client's task T have one key %q", label, k1)
	}
	if w := ask(t, n, SendRequest{Body: "count", Task: label}); w.State != store.Done || w.Reply != "1" {
		t.Fatalf("the user's task %q went on in the client's session: %s count %q %q", label, w.State, w.Reply, w.Error)
	}
}

// A one-off turn (session: message) holds its session: a message naming it by id (its id listed
// by the agent while the turn runs) waits for the turn, it does not prompt beside it.
func TestOneOffTurnHoldsItsSession(t *testing.T) {
	log := filepath.Join(t.TempDir(), "new")
	n := roundNode(t, map[string]string{"FAKE_ONE_PROMPT": t.TempDir(), "FAKE_NEW_LOG": log}, "concurrency: 2\nsession: message")
	a, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 800"})
	if err != nil {
		t.Fatal(err)
	}
	sid := newSessionID(t, log)
	time.Sleep(100 * time.Millisecond) // the turn is in its prompt
	b, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "echo hi", Session: sid})
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range waitAll(t, n, a.ID, b.ID) {
		if w.State != store.Done {
			t.Errorf("%s: %s %s %q: two prompts at once in the one-off session %s", w.Body, w.State, w.ErrorCode, w.Error, sid)
		}
	}
}

// A new session is the turn's from session/new on, through its (slow) setup: a message naming it
// by id meanwhile runs after the turn, in the session as the turn left it.
func TestNewSessionIsTheTurnsThroughItsSetup(t *testing.T) {
	log := filepath.Join(t.TempDir(), "new")
	n := roundNode(t, map[string]string{"FAKE_NEW_LOG": log, "FAKE_SLOW_SET": "700"}, "concurrency: 2\nmodel: fake-big")
	a, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "count", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	sid := newSessionID(t, log)
	time.Sleep(50 * time.Millisecond) // the turn is configuring the session
	b, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "count", Session: sid})
	if err != nil {
		t.Fatal(err)
	}
	got := waitAll(t, n, a.ID, b.ID)
	if got[0].Reply != "1" || got[1].Reply != "2" {
		t.Fatalf("task turn count %q, named turn count %q: the named one ran during the task's setup (want 1 then 2)", got[0].Reply, got[1].Reply)
	}
}

// capped holds to its cap for a real process's output: os/exec copies it with io.Copy, which
// prefers a ReadFrom of the writer to its Write.
func TestCappedHoldsForAProcess(t *testing.T) {
	c := &capped{max: 1000}
	cmd := exec.Command("sh", "-c", "head -c 200000 /dev/zero | tr '\\0' x >&2")
	cmd.Stderr = c
	if err := cmd.Run(); err != nil {
		t.Fatal(err)
	}
	if got := len(c.String()); got != 1000 {
		t.Fatalf("kept %d bytes of 200000, cap 1000", got)
	}
}

// A token shorter than 8 characters is still the node's: a copy of it under another name does
// not reach the agent.
func TestShortTokenCopyIsKeptFromAgents(t *testing.T) {
	t.Setenv("CI_SHORT_TOKEN", "abc123")
	t.Setenv("COPY_OF_TOKEN", "abc123")
	n := newNode(t, fmt.Sprintf(`
tokens:
  - {name: ci, token_env: CI_SHORT_TOKEN, send_to: ["*"]}
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1"}
    preamble: false
`, os.Args[0]))
	if w := ask(t, n, SendRequest{Body: "env COPY_OF_TOKEN"}); w.State != store.Done || w.Reply != "" {
		t.Fatalf("agent sees COPY_OF_TOKEN=%q (%s %q)", w.Reply, w.State, w.Error)
	}
}

// Listing sessions does not wait for a working slot whose process hangs in initialize (holding
// the slot's lock): it answers through a spare process within its context.
func TestSessionsPassAStartingSlot(t *testing.T) {
	hang := filepath.Join(t.TempDir(), "hang")
	n := roundNode(t, map[string]string{"FAKE_HANG_INIT": hang}, "")
	if _, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "echo hi"}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(hang); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slot 0's process never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	lctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	start := time.Now()
	go func() {
		_, _, err := n.Sessions(lctx, me, "fake", "", "")
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Sessions: %v", err)
		}
		if el := time.Since(start); el > 4*time.Second {
			t.Fatalf("Sessions took %s", el)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("Sessions waited for slot 0's hung initialize past its context")
	}
}

// A sandbox turn's replay that breaks off after part of its usage leaves the usage to the attempt
// that reads it whole: the turn counts 150 input tokens, not the 100 read before the break.
func TestSandboxPartialUsageDoesNotStand(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	item := func(id int64, role, text string) string {
		b, _ := json.Marshal(map[string]any{"id": id, "run_id": "r1", "role": role, "text": text})
		return string(b)
	}
	u7 := item(7, "log", `{"type":"usage","model":"m","input_tokens":100}`)
	u8 := item(8, "log", `{"type":"usage","model":"m","input_tokens":50}`)
	end9 := item(9, "log", `{"type":"acp.turn_end","stop_reason":"end_turn"}`)
	var mu sync.Mutex
	posted, broke := false, false
	h := func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sbtoken" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/sandboxes/sb1/messages"):
			w.Header().Set("X-Next-Cursor", "9")
			if r.URL.Query().Get("after") != "0" {
				w.Write([]byte("[]"))
				return
			}
			mu.Lock()
			breakNow := posted && !broke
			if breakNow {
				broke = true
			}
			mu.Unlock()
			if breakNow { // the first replay: one usage line, then the connection ends
				conn, buf, err := w.(http.Hijacker).Hijack()
				if err != nil {
					t.Error(err)
					return
				}
				body := "[" + u7 + ","
				fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nX-Next-Cursor: 9\r\nContent-Length: %d\r\n\r\n%s", len(body)+500, body)
				buf.Flush()
				conn.Close()
				return
			}
			w.Write([]byte("[" + u7 + "," + u8 + "," + end9 + "]"))
		case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/sandboxes/sb1/messages"):
			var body struct{ Text string }
			json.NewDecoder(r.Body).Decode(&body)
			mu.Lock()
			posted = true
			mu.Unlock()
			w.WriteHeader(202)
			json.NewEncoder(w).Encode(map[string]any{"run": map[string]any{"id": "r1", "status": "running", "task": body.Text, "session": "s-9"}, "delivery": "input"})
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/messages/stream"):
			w.Header().Set("Content-Type", "text/event-stream")
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		default:
			f.handler(w, r)
		}
	}
	n, _ := sandboxNodeOn(t, h, "store: \"memory:\"\nlimits: {max_attempts: 3}\n")
	m, _, err := n.Send(ctx, me, SendRequest{To: "cloud", Body: "count me"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Done {
		t.Fatalf("%s %s %q attempts=%d %v", w.State, w.ErrorCode, w.Error, w.Attempts, err)
	}
	if w.Attempts < 2 {
		t.Fatalf("attempts %d: the replay did not break", w.Attempts)
	}
	sums, err := n.st.UsageOf(ctx, store.UsageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sums) != 1 || sums[0].Turns != 1 || sums[0].Input != 150 {
		t.Fatalf("usage %+v: one turn of 150 input tokens", sums)
	}
}
