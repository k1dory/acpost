package node

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A setting cut short by the attempt's deadline takes the process with it: applied late by the
// agent, it would leave the process's idea of the session behind, and the next turn's explicit
// setting would be skipped as already there.
func TestSetupTimeoutLeavesNoStaleSettings(t *testing.T) {
	n := roundNode(t, map[string]string{"FAKE_SLOW_SET": "1000", "FAKE_SLOW_ONCE": filepath.Join(t.TempDir(), "slow")},
		"turn_timeout: 400ms\nmodel: fake-big")
	w := ask(t, n, SendRequest{Body: "settings", Task: "T"})
	if w.State != store.Failed || w.ErrorCode != "turn_timeout" {
		t.Fatalf("first turn: %s %s %q (want failed turn_timeout)", w.State, w.ErrorCode, w.Error)
	}
	time.Sleep(1200 * time.Millisecond) // the slow setting lands, if its process is still there
	w = ask(t, n, SendRequest{Body: "settings", Task: "T", Model: "fake-small"})
	if w.State != store.Done || !strings.Contains(w.Reply, "model=fake-small") {
		t.Fatalf("second turn asked for fake-small: %s %q %q", w.State, w.Reply, w.Error)
	}
}

// A message whose task's session is open in the process of the very turn that sent it (codex-like
// agent: one process per session) is refused at once: that turn waits for the answer and never
// ends first.
func TestSessionHeldByItsSenderIsRefused(t *testing.T) {
	n := roundNode(t, map[string]string{"FAKE_EXCLUSIVE": "1"}, "concurrency: 2")
	if w := ask(t, n, SendRequest{Body: "count", Task: "B"}); w.Reply != "1" {
		t.Fatalf("task B: %s %q %q", w.State, w.Reply, w.Error)
	}
	sidB := storedSession(t, n, "task:B")
	parent, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 5000", Task: "A"})
	if err != nil {
		t.Fatal(err)
	}
	a := n.acp["fake"]
	slot := -1
	var pa Principal
	for deadline := time.Now().Add(10 * time.Second); slot < 0; {
		for i, s := range a.slots {
			if p, ok := n.PrincipalForToken(s.token); ok && n.currentOf(p) == parent.ID {
				slot, pa = i, p
			}
		}
		if time.Now().After(deadline) {
			t.Fatal("task A's turn never started")
		}
		time.Sleep(5 * time.Millisecond)
	}
	// Task B's session was opened last in the process of the slot that runs A (as if A's slot had run
	// B before): the agent keeps it to that process.
	s := a.slots[slot]
	s.mu.Lock()
	pid := s.proc.cmd.Process.Pid
	s.mu.Unlock()
	if err := os.WriteFile(filepath.Join(n.cfg.Agents["fake"].Env["FAKE_STATE"], sidB+".lock"), []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	a.setOwner(sidB, slot)
	child, _, err := n.Send(ctx, pa, SendRequest{To: "fake", Body: "count", Task: "B"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, child.ID, 3*time.Second)
	if err != nil || w.State != store.Failed || w.ErrorCode != "refused" {
		t.Fatalf("the message from task A to task B: %s %s %q %v (want refused at once, not a wait for A)", w.State, w.ErrorCode, w.Error, err)
	}
}

// breakingSandbox serves a sandbox whose turn r1 has used 100 then 50 input tokens: the first
// replay of its history breaks off after the first usage line; with failAfter, every read of the
// history after that answers 503.
func breakingSandbox(t *testing.T, failAfter bool) http.HandlerFunc {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	item := func(id int64, text string) string {
		b, _ := json.Marshal(map[string]any{"id": id, "run_id": "r1", "role": "log", "text": text})
		return string(b)
	}
	u7 := item(7, `{"type":"usage","model":"m","input_tokens":100}`)
	u8 := item(8, `{"type":"usage","model":"m","input_tokens":50}`)
	end9 := item(9, `{"type":"acp.turn_end","stop_reason":"end_turn"}`)
	var mu sync.Mutex
	posted, broke := false, false
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer sbtoken" {
			w.WriteHeader(401)
			return
		}
		switch {
		case r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/sandboxes/sb1/messages"):
			mu.Lock()
			breakNow := posted && !broke && r.URL.Query().Get("after") == "0"
			gone := failAfter && broke
			if breakNow {
				broke = true
			}
			mu.Unlock()
			if gone {
				w.WriteHeader(503)
				return
			}
			w.Header().Set("X-Next-Cursor", "9")
			if r.URL.Query().Get("after") != "0" {
				w.Write([]byte("[]"))
				return
			}
			if breakNow {
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
}

func inputUsage(t *testing.T, n *Node) int64 {
	t.Helper()
	sums, err := n.st.UsageOf(ctx, store.UsageQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var total int64
	for _, s := range sums {
		total += s.Input
	}
	return total
}

// The last attempt's release fails the message: whoever sees it failed (a follower, the hub's
// relay, flushing the events and leaving) already finds the turn's usage, event and count. A
// trigger in the node's database notes, in the very write that makes the message failed, what of
// the usage is there.
func TestSandboxFinalStateCarriesItsUsage(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "acpost.db")
	n, _ := sandboxNodeOn(t, breakingSandbox(t, false), fmt.Sprintf("store: %q\nlimits: {max_attempts: 1}\n", "sqlite://"+path))
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)") // the node writes as it starts
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE seen_final (msg TEXT, counted INTEGER, events INTEGER);
		CREATE TRIGGER seen_final AFTER UPDATE OF state ON messages WHEN NEW.state = 'failed' BEGIN
			INSERT INTO seen_final VALUES (NEW.id, (SELECT count(*) FROM turn_usage WHERE msg = NEW.id),
				(SELECT count(*) FROM events WHERE msg = NEW.seq AND kind = 'usage'));
		END;`); err != nil {
		t.Fatal(err)
	}
	m, _, err := n.Send(ctx, me, SendRequest{To: "cloud", Body: "count me"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Failed {
		t.Fatalf("%s %s %q %v (want failed)", w.State, w.ErrorCode, w.Error, err)
	}
	var counted, events int
	if err := db.QueryRow("SELECT counted, events FROM seen_final WHERE msg = ?", m.ID).Scan(&counted, &events); err != nil {
		t.Fatal(err)
	}
	if counted != 1 || events != 1 {
		t.Fatalf("as the message turned failed: usage counted %d, usage events %d (want 1, 1: there before the outcome)", counted, events)
	}
	if used := inputUsage(t, n); used != 100 {
		t.Fatalf("%d input tokens counted, want 100", used)
	}
}

// What an attempt read of its turn's usage before its replay broke off stays counted when the
// attempts after it fail before reading the turn again (sandboxd's history unavailable).
func TestSandboxPartialUsageOutlivesFailedAttempts(t *testing.T) {
	n, _ := sandboxNodeOn(t, breakingSandbox(t, true), "store: \"memory:\"\nlimits: {max_attempts: 2}\n")
	m, _, err := n.Send(ctx, me, SendRequest{To: "cloud", Body: "count me"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Failed || w.Attempts != 2 {
		t.Fatalf("%s %s %q attempts=%d %v (want failed after 2)", w.State, w.ErrorCode, w.Error, w.Attempts, err)
	}
	if used := inputUsage(t, n); used != 100 {
		t.Fatalf("%d input tokens counted: the 100 the first attempt read are lost", used)
	}
}
