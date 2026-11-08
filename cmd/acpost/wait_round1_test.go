package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A wait whose long poll the node answers after the deadline ends as any wait that timed out: the
// message's text so far, the hint to go on, exit 4; with --json the message on stdout.
func TestWaitTimeoutOnTheWireIsThePendingCase(t *testing.T) {
	running := store.Message{ID: "msg_1", State: store.Running, Reply: "half"}
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/wait"):
			time.Sleep(300 * time.Millisecond) // later than the 60ms the CLI waits
		case r.URL.Path == "/v1/messages/msg_1":
		default:
			w.WriteHeader(404)
			return
		}
		json.NewEncoder(w).Encode(running)
	}))
	defer node.Close()
	e := newEnv(t, inbox)
	r := e.run("", "--node", node.URL, "wait", "msg_1", "--timeout", "60ms")
	if r.code != exitTimeout || !strings.Contains(r.err, "acpost wait msg_1") || r.out != "half" {
		t.Fatalf("exit %d, stdout %q, stderr %s", r.code, r.out, r.err)
	}
	r = e.run("", "--node", node.URL, "--json", "wait", "msg_1", "--timeout", "60ms")
	var m store.Message
	if r.code != exitTimeout || json.Unmarshal([]byte(r.out), &m) != nil || m.ID != "msg_1" || !strings.Contains(r.err, `"timeout"`) {
		t.Fatalf("--json: exit %d, stdout %q, stderr %s", r.code, r.out, r.err)
	}
}
