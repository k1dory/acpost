package node

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// The cloud: its sandboxes are chats, its catalogue is what a cloud task starts from; a new task
// is a sandbox whose agent takes the messages; a sandbox's history reads as events.
func TestCloud(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{}), id: "sb_abc123"}
	f.history = []map[string]any{
		{"id": 2, "run_id": "r0", "role": "user", "text": "earlier question"},
		{"id": 3, "run_id": "r0", "role": "assistant", "text": "earlier answer"},
		{"id": 4, "run_id": "r0", "role": "tool", "text": "Bash: ls"},
	}
	f.frames = []string{
		"USER:6",
		frame(7, "r1", "assistant", "cloud answer", nil),
		frame(8, "r1", "log", `{"type":"acp.turn_end","stop_reason":"end_turn"}`, nil),
	}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	env := filepath.Join(t.TempDir(), "pilot.env")
	os.WriteFile(env, []byte("export SANDBOXD_URL="+srv.URL+"\nexport SANDBOXD_TOKEN='sbtoken'\n"), 0o600)
	n := newNode(t, "cloud: {env_file: "+env+"}\n")

	agents, err := n.CloudAgents(ctx, me)
	if err != nil || len(agents) != 2 || agents[0].Ref != "assistant" || agents[1].Ref != "shared/reviewer" {
		t.Fatalf("%+v %v", agents, err)
	}
	chats, err := n.Chats(ctx, me)
	if err != nil || len(chats.Items) != 1 || chats.Items[0].Key != "cloud:sb_abc123" || chats.Items[0].Status != "sleeping" || chats.Items[0].CloudAgent != "assistant" {
		t.Fatalf("%+v %v", chats, err)
	}
	evs, err := n.CloudHistory(ctx, me, "sb_abc123")
	if err != nil || len(evs) != 3 || evs[0].Kind != "user" || evs[1].Kind != "text" || evs[2].Kind != "tool_call" {
		t.Fatalf("%+v %v", evs, err)
	}

	task, err := n.CreateCloudTask(ctx, me, client.CloudTask{Agent: "assistant", Name: "Fix the parser!", Repo: "https://git.example/o/r"})
	if err != nil || task.Sandbox != "sb_abc123" {
		t.Fatalf("%+v %v", task, err)
	}
	f.mu.Lock()
	created := f.created[0]
	f.mu.Unlock()
	if name, _ := created["name"].(string); !strings.HasPrefix(name, "fix-the-parser-") || created["agent_ref"] != "assistant" || created["repo"] != "https://git.example/o/r" {
		t.Fatalf("created %v", created)
	}
	m, _, err := n.Send(ctx, me, SendRequest{To: "agent:" + task.Sandbox, Body: "go"})
	if err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Done || w.Reply != "cloud answer" {
		t.Fatalf("%+v %v", w, err)
	}
	// The sandbox has a thread now: one chat, the thread.
	chats, _ = n.Chats(ctx, me)
	if len(chats.Items) != 1 || chats.Items[0].Kind != "thread" {
		t.Fatalf("%+v", chats.Items)
	}
	// Not for a client; not a sandbox the tenant has not.
	if _, _, err := n.Send(ctx, Principal{Kind: "client", Name: "ci"}, SendRequest{To: "agent:sb_other", Body: "x"}); code(err) != "refused" {
		t.Fatalf("a client reached the cloud: %v", err)
	}
	if _, _, err := n.Send(ctx, me, SendRequest{To: "agent:sb_missing", Body: "x"}); code(err) != "not_found" {
		t.Fatalf("a sandbox the tenant has not: %v", err)
	}
}
