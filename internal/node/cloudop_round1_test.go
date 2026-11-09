package node

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/k1dory/acpost/internal/client"
)

// namedSandboxd is a tenant that keeps the sandboxes made, by name.
type namedSandboxd struct {
	mu      sync.Mutex
	made    []map[string]any
	posts   int
	answers int // POSTs that make the sandbox, then fail to answer (500) — a lost answer
}

func (f *namedSandboxd) handler(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == "GET" && r.URL.Path == "/v1/sandboxes":
		_ = json.NewEncoder(w).Encode(f.made)
	case r.Method == "POST" && r.URL.Path == "/v1/sandboxes":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.posts++
		s := map[string]any{"id": fmt.Sprintf("sb_t%d", len(f.made)+1), "name": body["name"], "agent_ref": body["agent_ref"], "status": "creating"}
		f.made = append(f.made, s)
		if f.answers > 0 {
			f.answers--
			w.WriteHeader(500)
			return
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(s)
	default:
		w.WriteHeader(404)
	}
}

func cloudNode(t *testing.T, f *namedSandboxd, extra string) *Node {
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	env := filepath.Join(t.TempDir(), "pilot.env")
	os.WriteFile(env, []byte("export SANDBOXD_URL="+srv.URL+"\nexport SANDBOXD_TOKEN='sbtoken'\n"), 0o600)
	return newNode(t, extra+"cloud: {env_file: "+env+"}\n")
}

// A cloud task made, its answer lost: the retry with the same key takes the sandbox made, it does
// not make another.
func TestCloudTaskRetryTakesSandboxMade(t *testing.T) {
	f := &namedSandboxd{answers: 1}
	n := cloudNode(t, f, "")
	task := client.CloudTask{Agent: "assistant", Name: "Fix it", Idem: "op-1"}
	if _, err := n.CreateCloudTask(ctx, me, task); err == nil {
		t.Fatal("the lost answer did not fail the first try")
	}
	chat, err := n.CreateCloudTask(ctx, me, task)
	if err != nil {
		t.Fatal(err)
	}
	if f.posts != 1 || chat.Sandbox != "sb_t1" {
		t.Fatalf("%d sandboxes made; got %s", f.posts, chat.Sandbox)
	}
	// Another key, another sandbox.
	if other, err := n.CreateCloudTask(ctx, me, client.CloudTask{Agent: "assistant", Name: "Fix it", Idem: "op-2"}); err != nil || other.Sandbox == chat.Sandbox {
		t.Fatalf("%+v %v", other, err)
	}
}

// A handoff repeated with its key gives the same sandbox and message; one whose message would be
// refused makes no sandbox at all.
func TestHandoffRetryAndRefusalMakeNoSecondSandbox(t *testing.T) {
	f := &namedSandboxd{}
	n := cloudNode(t, f, inboxCfg)
	dir := handoffRepo(t)
	r := HandoffRequest{Cwd: dir, CloudAgent: "assistant", Note: "finish it", Idem: "h-1"}
	first, err := n.Handoff(ctx, me, r)
	if err != nil {
		t.Fatal(err)
	}
	again, err := n.Handoff(ctx, me, r)
	if err != nil || again.Chat.Sandbox != first.Chat.Sandbox || again.Message != first.Message || f.posts != 1 {
		t.Fatalf("repeat: %+v %v (%d sandboxes made)", again, err, f.posts)
	}
	r.Note, r.Idem = strings.Repeat("x", 2048), "h-2" // over inboxCfg's max_body of 1 KiB
	if _, err := n.Handoff(ctx, me, r); code(err) != "refused" {
		t.Fatalf("an oversized handoff: %v", err)
	}
	if f.posts != 1 {
		t.Fatalf("a refused handoff made a sandbox (%d)", f.posts)
	}
}
