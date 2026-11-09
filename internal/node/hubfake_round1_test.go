package node

import (
	"encoding/json"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// fakeHub is a hub's side of one delivered message, as a joined node sees it: the lease it takes,
// the state its copy is in, the replies it got. down makes it drop every connection.
type fakeHub struct {
	mu      sync.Mutex
	lease   string      // the lease the hub takes now ("" none)
	state   store.State // its copy's state
	errText string      // its copy's error
	replies []client.ReplyRequest
	extends map[string]int
	calls   map[string]int // by the path's last part
	byLease map[string]int // reply calls by lease
	down    bool
	hang    bool // accept and never answer (until the request's context ends)
	srv     *httptest.Server
	quit    chan struct{} // closed at the test's end: hung requests end
	missing bool          // GET of a message it was not sent: not_found

	// Sends: what it was sent, and the messages it keeps (by id; by sender and key, as a hub
	// keeps a repeat of a key to its first send).
	sent       []client.SendRequest
	msgs       map[string]store.Message
	keys       map[string]string
	refuse     int           // the next sends refused (403)
	loseAnswer int           // the next sends taken, the answer lost (the connection dropped)
	hold       chan struct{} // a send taken waits for it before answering
	received   chan struct{} // told when a send is taken
	getFails   int           // the next GETs of a message answer 500
	cancels    []string
	holdIn     chan struct{} // a send waits for it before the hub takes it
	reply      string        // its copy's reply
	files      int           // GETs of files
	hangFiles  int           // the next GETs of files never answer
}

func newFakeHub(t *testing.T, lease string) *fakeHub {
	f := &fakeHub{lease: lease, state: store.Running, extends: map[string]int{}, calls: map[string]int{}, byLease: map[string]int{}, quit: make(chan struct{}),
		msgs: map[string]store.Message{}, keys: map[string]string{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(func() { close(f.quit); f.srv.Close() })
	return f
}

func (f *fakeHub) client(t *testing.T) *client.Client {
	c, err := client.New(f.srv.URL, "dummy", "")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func conflict(w http.ResponseWriter, msg string) {
	w.WriteHeader(http.StatusConflict)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "conflict", "message": msg}})
}

func (f *fakeHub) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	part := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
	f.calls[part]++
	down, hang := f.down, f.hang
	f.mu.Unlock()
	if hang {
		select {
		case <-r.Context().Done():
		case <-f.quit:
		}
		return
	}
	if down {
		hj, _ := w.(http.Hijacker)
		c, _, _ := hj.Hijack()
		c.Close()
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case part == "lease":
		l := r.URL.Query().Get("lease")
		f.extends[l]++
		if l != f.lease || f.state != store.Running {
			conflict(w, "lease lost")
			return
		}
		w.WriteHeader(204)
	case part == "reply":
		var rr client.ReplyRequest
		_ = json.NewDecoder(r.Body).Decode(&rr)
		f.byLease[rr.Lease]++
		if rr.Lease != f.lease || f.state != store.Running {
			conflict(w, "lease lost")
			return
		}
		f.replies = append(f.replies, rr)
		f.state, f.lease = store.Done, ""
		if rr.Error != "" {
			f.state = store.Failed
		}
		w.WriteHeader(204)
	case part == "events":
		var rq client.RelayRequest
		_ = json.NewDecoder(r.Body).Decode(&rq)
		_ = json.NewEncoder(w).Encode(map[string]int64{"relayed": rq.After + int64(len(rq.Events))})
	case r.Method == "POST" && part == "messages":
		var rq client.SendRequest
		_ = json.NewDecoder(r.Body).Decode(&rq)
		key := r.Header.Get("Idempotency-Key")
		if f.refuse > 0 {
			f.refuse--
			w.WriteHeader(http.StatusForbidden)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "refused", "message": "not now"}})
			return
		}
		if holdIn := f.holdIn; holdIn != nil {
			f.mu.Unlock()
			select {
			case <-holdIn:
			case <-f.quit:
			}
			f.mu.Lock()
		}
		f.sent = append(f.sent, rq)
		m, dup := f.msgs[f.keys[rq.From+"|"+key]], key != "" && f.keys[rq.From+"|"+key] != ""
		if !dup {
			m = store.Message{ID: rq.ID, From: rq.From, To: rq.To + "@far", Body: rq.Body, Thread: rq.ID, State: store.Queued}
			f.msgs[rq.ID] = m
			if key != "" {
				f.keys[rq.From+"|"+key] = rq.ID
			}
		}
		if f.received != nil {
			select {
			case f.received <- struct{}{}:
			default:
			}
		}
		if hold := f.hold; hold != nil {
			f.mu.Unlock()
			select {
			case <-hold:
			case <-f.quit:
			}
			f.mu.Lock()
		}
		if f.loseAnswer > 0 {
			f.loseAnswer--
			hj, _ := w.(http.Hijacker)
			c, _, _ := hj.Hijack()
			c.Close()
			return
		}
		if dup {
			w.WriteHeader(http.StatusOK)
		} else {
			w.WriteHeader(http.StatusCreated)
		}
		_ = json.NewEncoder(w).Encode(m)
	case r.Method == "POST" && part == "cancel":
		id := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/v1/messages/"), "/cancel")
		f.cancels = append(f.cancels, id)
		if f.state.Final() {
			conflict(w, "message is already "+string(f.state))
			return
		}
		f.state = store.Cancelled
		_ = json.NewEncoder(w).Encode(store.Message{ID: id, State: store.Cancelled})
	case r.Method == "GET" && part == "by-idem":
		q := r.URL.Query()
		id := f.keys[q.Get("from")+"|"+q.Get("key")]
		if id == "" {
			w.WriteHeader(http.StatusNotFound)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "not_found", "message": "none"}})
			return
		}
		_ = json.NewEncoder(w).Encode(f.msgs[id])
	case part == "wait":
		f.mu.Unlock()
		select {
		case <-time.After(100 * time.Millisecond):
		case <-r.Context().Done():
		}
		f.mu.Lock()
		_ = json.NewEncoder(w).Encode(store.Message{ID: r.PathValue("id"), State: f.state, Reply: f.reply})
	case r.Method == "GET" && strings.HasPrefix(part, "msg_") && f.getFails > 0:
		f.getFails--
		w.WriteHeader(500)
	case r.Method == "GET" && strings.HasPrefix(part, "msg_") && f.msgs[part].ID != "":
		m := f.msgs[part]
		m.State, m.Reply = f.state, f.reply
		_ = json.NewEncoder(w).Encode(m)
	case r.Method == "GET" && part == "files":
		f.files++
		if f.hangFiles > 0 {
			f.hangFiles--
			f.mu.Unlock()
			select {
			case <-r.Context().Done():
			case <-f.quit:
			}
			f.mu.Lock()
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{{"name": "f.bin", "size": 1, "data": "eA=="}}})
	case r.Method == "GET" && strings.HasPrefix(part, "msg_") && f.missing:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "not_found", "message": "no message " + part}})
	case r.Method == "GET" && strings.HasPrefix(part, "msg_"):
		_ = json.NewEncoder(w).Encode(store.Message{ID: part, State: f.state, Error: f.errText})
	case part == "claim": // nothing to hand: a long poll, short
		f.mu.Unlock()
		select {
		case <-time.After(100 * time.Millisecond):
		case <-r.Context().Done():
		}
		f.mu.Lock()
		w.WriteHeader(204)
	default:
		w.WriteHeader(204)
	}
}

func (f *fakeHub) replyCalls(lease string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.byLease[lease]
}

func (f *fakeHub) set(fn func(f *fakeHub)) {
	f.mu.Lock()
	fn(f)
	f.mu.Unlock()
}

func (f *fakeHub) get() fakeHub {
	f.mu.Lock()
	defer f.mu.Unlock()
	return fakeHub{lease: f.lease, state: f.state, replies: append([]client.ReplyRequest(nil), f.replies...),
		sent: append([]client.SendRequest(nil), f.sent...), cancels: append([]string(nil), f.cancels...), msgs: maps.Clone(f.msgs), files: f.files}
}

func (f *fakeHub) count(part string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[part]
}

// fastHub makes relays renew the hub's lease every d, and try a reply again after d/3, for this
// test.
func fastHub(t *testing.T, d time.Duration) {
	oldRenew, oldRetry, oldAbsence := hubRenew, hubRetry, hubAbsence
	hubRenew, hubRetry, hubAbsence = d, d/3, 10*d
	t.Cleanup(func() { hubRenew, hubRetry, hubAbsence = oldRenew, oldRetry, oldAbsence })
}

// eventually waits up to 5 s for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	eventuallyWithin(t, 5*time.Second, what, cond)
}

func eventuallyWithin(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatal("timed out: " + what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

const hubCfg = `
node: {name: edge}
limits: {max_queued_per_agent: 3}
agents:
  bot: {driver: inbox, lease: 1m}
`
