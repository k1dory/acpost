package node

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// unfinishedSandbox serves a sandbox whose turn r1 has used 100, then 50 input tokens and goes on:
// the first replay breaks off after the first usage line, the next reads both and the turn has not
// ended yet (its stream waits).
func unfinishedSandbox(t *testing.T) http.HandlerFunc {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	item := func(id int64, text string) string {
		b, _ := json.Marshal(map[string]any{"id": id, "run_id": "r1", "role": "log", "text": text})
		return string(b)
	}
	u7 := item(7, `{"type":"usage","model":"m","input_tokens":100}`)
	u8 := item(8, `{"type":"usage","model":"m","input_tokens":50}`)
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
			if breakNow {
				broke = true
			}
			mu.Unlock()
			w.Header().Set("X-Next-Cursor", "8")
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
				fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nX-Next-Cursor: 8\r\nContent-Length: %d\r\n\r\n%s", len(body)+500, body)
				buf.Flush()
				conn.Close()
				return
			}
			w.Write([]byte("[" + u7 + "," + u8 + "]"))
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

// A retry reads more of the turn (150 tokens, the first attempt broke off at 100) and the message
// is cancelled while the turn goes on: the count, the published event and what a receiving node
// counts from it are all 150.
func TestCancelledRetryPublishesWhatItRead(t *testing.T) {
	n, _ := sandboxNodeOn(t, unfinishedSandbox(t), "store: \"memory:\"\nlimits: {max_attempts: 3}\n")
	m, _, err := n.Send(ctx, me, SendRequest{To: "cloud", Body: "count me"})
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(10 * time.Second); ; time.Sleep(5 * time.Millisecond) {
		got, err := n.st.Get(ctx, m.ID)
		if err == nil && got.State == store.Running && got.Attempts == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the retry never ran: %s attempts=%d", got.State, got.Attempts)
		}
	}
	time.Sleep(500 * time.Millisecond) // the retry has read the history (both usage lines) and follows the turn
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(500 * time.Millisecond) // the attempt sees the cancel and ends
	var events []string
	evs, err := n.st.Events(ctx, m.ID, 0, 1000)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.Kind == "usage" {
			events = append(events, e.Data)
		}
	}
	if len(events) != 1 {
		t.Fatalf("usage events %q, want one", events)
	}
	var u store.Usage
	if err := json.Unmarshal([]byte(events[0]), &u); err != nil {
		t.Fatal(err)
	}
	if counted := inputUsage(t, n); u.Input != 150 || counted != 150 {
		t.Fatalf("the event says %d input tokens, this node counts %d: want 150 and 150", u.Input, counted)
	}
	// A receiving node counts what the event says.
	r, err := store.Open("memory:", store.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	rm := store.Message{ID: m.ID, From: "user:a", To: "agent:cloud@n", Body: "hi"}
	if _, _, err := r.Insert(ctx, &rm); err != nil {
		t.Fatal(err)
	}
	if err := r.AddEvent(ctx, m.ID, "usage", events[0]); err != nil {
		t.Fatal(err)
	}
	if sums, err := r.UsageOf(ctx, store.UsageQuery{}); err != nil || len(sums) != 1 || sums[0].Input != 150 {
		t.Fatalf("the receiving node counts %+v %v, want 150", sums, err)
	}
}
