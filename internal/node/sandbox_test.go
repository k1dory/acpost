package node

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
	"github.com/k1dory/acpx"
)

// fakeSandboxd is sandboxd's conversation API for one sandbox: frames is what the stream says
// after the message is posted (a frame "BREAK" ends the connection there; "WAIT" waits for an
// answer to a request).
type fakeSandboxd struct {
	t        *testing.T
	mu       sync.Mutex
	posted   []string // texts
	keys     []string // Idempotency-Keys
	answers  []map[string]any
	answered chan struct{}
	afters   []string // ?after of each stream connection
	frames   []string
	next     int
}

func (f *fakeSandboxd) handler(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer sbtoken" {
		w.WriteHeader(401)
		return
	}
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/sandboxes/sb1/messages":
		// Where the stream stands: event 5.
		cur := "5"
		if r.URL.Query().Get("after") == "5" {
			cur = "5"
		}
		w.Header().Set("X-Next-Cursor", cur)
		w.Write([]byte("[]"))
	case r.Method == "POST" && r.URL.Path == "/v1/sandboxes/sb1/messages":
		var body struct{ Text string }
		json.NewDecoder(r.Body).Decode(&body)
		f.mu.Lock()
		f.posted = append(f.posted, body.Text)
		f.keys = append(f.keys, r.Header.Get("Idempotency-Key"))
		f.mu.Unlock()
		w.WriteHeader(202)
		json.NewEncoder(w).Encode(map[string]any{"run": map[string]any{"id": "r1", "status": "running", "session": "s-9"}, "delivery": "resumed"})
	case r.Method == "POST" && strings.HasPrefix(r.URL.Path, "/v1/sandboxes/sb1/requests/"):
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		body["request"] = strings.TrimPrefix(r.URL.Path, "/v1/sandboxes/sb1/requests/")
		f.mu.Lock()
		f.answers = append(f.answers, body)
		f.mu.Unlock()
		close(f.answered)
		w.WriteHeader(202)
	case r.Method == "POST" && strings.HasSuffix(r.URL.Path, "/cancel"):
		w.Write([]byte(`{"id":"r1","status":"cancelled"}`))
	case r.Method == "GET" && r.URL.Path == "/v1/sandboxes/sb1/messages/stream":
		f.mu.Lock()
		f.afters = append(f.afters, r.URL.Query().Get("after"))
		f.mu.Unlock()
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		for {
			f.mu.Lock()
			if f.next >= len(f.frames) {
				f.mu.Unlock()
				<-r.Context().Done()
				return
			}
			fr := f.frames[f.next]
			f.next++
			f.mu.Unlock()
			if id, ok := strings.CutPrefix(fr, "USER:"); ok { // the conversation's turn of what was posted
				f.mu.Lock()
				text := f.posted[len(f.posted)-1]
				f.mu.Unlock()
				var n int64
				fmt.Sscan(id, &n)
				fr = frame(n, "r1", "user", text, nil)
			}
			switch fr {
			case "BREAK":
				return
			case "WAIT":
				select {
				case <-f.answered:
				case <-time.After(10 * time.Second):
					f.t.Error("no answer reached the sandbox")
				}
				continue
			}
			io.WriteString(w, fr)
			fl.Flush()
		}
	default:
		w.WriteHeader(404)
	}
}

func frame(id int64, run, role, text string, data any) string {
	m := map[string]any{"id": id, "run_id": run, "role": role, "text": text}
	if data != nil {
		m["data"] = data
	}
	b, _ := json.Marshal(m)
	return fmt.Sprintf("event: %s\nid: %d\ndata: %s\n\n", role, id, b)
}

func sandboxNode(t *testing.T, f *fakeSandboxd) *Node {
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	t.Setenv("SB_TOKEN", "sbtoken")
	return newNode(t, `
agents:
  cloud:
    driver: sandbox
    turn_timeout: 30s
    sandbox: {url: "`+srv.URL+`", id: sb1, token_env: SB_TOKEN}
`)
}

// A message is the sandbox's next message; what its run says becomes the message's events and
// its reply; a request of the sandbox is the message's permission request, answered here and
// handed to the sandbox; a broken stream is opened again where it stopped.
func TestSandboxTurn(t *testing.T) {
	req := map[string]any{"type": "acp.request", "request": map[string]any{"request_id": "req_00000000000000aa", "kind": "permission", "title": "Run rm -rf build",
		"options": []map[string]string{{"option_id": "allow", "name": "Allow", "kind": "allow_once"}, {"option_id": "no", "name": "No", "kind": "reject_once"}}}}
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	f.frames = []string{
		frame(3, "r0", "assistant", "an old turn", nil), // before the cursor: replayed, not ours
		frame(6, "r0", "assistant", "another run", nil),
		// The run's earlier turn, still ending when this message came: not this message's.
		frame(7, "r1", "assistant", "the earlier turn's answer", nil),
		frame(8, "r1", "log", `{"type":"acp.turn_end","stop_reason":"end_turn"}`, nil),
		"USER:9",
		frame(10, "r1", "assistant", "Looking at the build.", nil),
		"BREAK",
		frame(11, "r1", "tool", "Bash: make test", nil),
		frame(12, "r1", "log", `{"type":"usage","model":"claude-x","input_tokens":100,"output_tokens":20,"cache_read_tokens":300,"cost_usd":0.25}`, nil),
		frame(13, "r1", "request", "Run rm -rf build", req),
		"WAIT",
		frame(14, "r1", "resolved", "selected (user)", map[string]any{"type": "acp.request_resolved", "request_id": "req_00000000000000aa", "outcome": "selected", "by": "user"}),
		frame(15, "r1", "assistant", "All green.", nil),
		frame(16, "r1", "log", `{"type":"acp.turn_end","stop_reason":"end_turn"}`, nil),
	}
	n := sandboxNode(t, f)
	m, _, err := n.Send(ctx, me, SendRequest{To: "cloud", Body: "fix the build"})
	if err != nil {
		t.Fatal(err)
	}
	// The sandbox's request waits here: answer it.
	var asked string
	for deadline := time.Now().Add(10 * time.Second); asked == "" && time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		evs, _ := n.Events(ctx, me, m.ID, 0, 100)
		for _, e := range evs {
			if e.Kind == "permission_request" {
				var r acpx.Request
				json.Unmarshal([]byte(e.Data), &r)
				asked = r.ID
			}
		}
	}
	if asked == "" {
		t.Fatal("no permission request")
	}
	if err := n.Answer(ctx, me, m.ID, acpx.Reply{RequestID: asked, OptionID: "nope"}); code(err) != "invalid" {
		t.Fatalf("an option not offered: %v", err)
	}
	if err := n.Answer(ctx, me, m.ID, acpx.Reply{RequestID: asked, OptionID: "allow"}); err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Done {
		t.Fatalf("%+v %v", w, err)
	}
	if w.Reply != "All green." {
		t.Fatalf("reply %q", w.Reply)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.posted) != 1 || !strings.Contains(f.posted[0], "fix the build") || f.keys[0] != "acpost-"+m.ID {
		t.Fatalf("posted %q keys %q", f.posted, f.keys)
	}
	if len(f.answers) != 1 || f.answers[0]["run_id"] != "r1" || f.answers[0]["option_id"] != "allow" || f.answers[0]["request"] != "req_00000000000000aa" {
		t.Fatalf("answers %v", f.answers)
	}
	if len(f.afters) != 2 || f.afters[0] != "5" || f.afters[1] != "10" {
		t.Fatalf("stream connections after %v: once from the cursor, again from where it broke", f.afters)
	}
	evs, _ := n.Events(ctx, me, m.ID, 0, 100)
	var kinds []string
	var text strings.Builder
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
		if e.Kind == "text" {
			text.WriteString(e.Data)
		}
		if e.Kind == "usage" {
			var u store.Usage
			json.Unmarshal([]byte(e.Data), &u)
			if u.Input != 100 || u.CacheRead != 300 || u.Cost == nil || *u.Cost != 0.25 || u.Model != "claude-x" || u.Session != "sandbox:sb1/s-9" {
				t.Fatalf("usage %+v", u)
			}
		}
	}
	all := strings.Join(kinds, " ")
	for _, k := range []string{"tool_call", "permission_request", "permission", "usage", "text"} {
		if !strings.Contains(all, k) {
			t.Fatalf("no %s event in %s", k, all)
		}
	}
	if strings.Contains(text.String(), "old turn") || strings.Contains(text.String(), "another run") || strings.Contains(text.String(), "earlier turn") {
		t.Fatalf("another run's text in the turn: %s", text.String())
	}
}

// A run that fails ends the turn failed, with what the agent said.
func TestSandboxRunFailed(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	f.frames = []string{
		"USER:6",
		frame(7, "r1", "assistant", "Trying.", nil),
		frame(8, "r1", "system", "run failed", nil),
	}
	n := sandboxNode(t, f)
	m, _, _ := n.Send(ctx, me, SendRequest{To: "cloud", Body: "go"})
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Failed || w.ErrorCode != "agent_stopped" || w.Reply != "Trying." {
		t.Fatalf("%+v %v", w, err)
	}
}

// A token sandboxd refuses fails the message, not retried.
func TestSandboxRefused(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	t.Setenv("SB_TOKEN", "wrong")
	n := newNode(t, `
agents:
  cloud:
    driver: sandbox
    sandbox: {url: "`+srv.URL+`", id: sb1, token_env: SB_TOKEN}
`)
	m, _, _ := n.Send(ctx, me, SendRequest{To: "cloud", Body: "go"})
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Failed || w.ErrorCode != "refused" {
		t.Fatalf("%+v %v", w, err)
	}
}

// A run that ends before the conversation took the message fails the message: nothing of an
// earlier turn is its reply.
func TestSandboxRunEndedFirst(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{})}
	f.frames = []string{
		frame(6, "r1", "assistant", "the earlier turn", nil),
		frame(7, "r1", "system", "run done", nil),
	}
	n := sandboxNode(t, f)
	m, _, _ := n.Send(ctx, me, SendRequest{To: "cloud", Body: "go"})
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Failed || w.Reply != "" || !strings.Contains(w.Error, "before it took the message") {
		t.Fatalf("%+v %v", w, err)
	}
}
