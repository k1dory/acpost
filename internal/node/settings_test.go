package node

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// A new task runs in the directory it names; the thread's next messages stay there; a directory
// that is not one, or a sender that is not the node's, is refused.
func TestTaskCwd(t *testing.T) {
	n := acpNode(t, "session: thread")
	dir, _ := filepath.EvalSymlinks(t.TempDir())
	m := ask(t, n, SendRequest{Body: "cwd", Cwd: dir})
	if m.Reply != dir {
		t.Fatalf("session cwd %q, want %q", m.Reply, dir)
	}
	next := ask(t, n, SendRequest{Body: "cwd", Thread: m.Thread})
	if next.Reply != dir {
		t.Fatalf("the thread's next message in %q", next.Reply)
	}
	if _, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "cwd", Cwd: "relative/dir"}); code(err) != "invalid" {
		t.Fatalf("relative: %v", err)
	}
	if _, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "cwd", Cwd: filepath.Join(dir, "missing")}); code(err) != "invalid" {
		t.Fatalf("missing: %v", err)
	}
	if _, _, err := n.Send(ctx, Principal{Kind: "client", Name: "ci"}, SendRequest{To: "fake", Body: "cwd", Cwd: dir}); code(err) != "refused" {
		t.Fatalf("a client chose the directory: %v", err)
	}
	if _, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "x", Meta: map[string]string{"acp_cwd": "/"}}); code(err) != "invalid" {
		t.Fatalf("acp_cwd set by hand: %v", err)
	}
}

// The model and reasoning level a message asks for are selected in its session before its turn;
// the agent's options list what can be chosen; a value it does not offer is a notice, not a failure.
func TestMessageSettings(t *testing.T) {
	t.Setenv("FAKE_COMMANDS", "1") // the agent says its slash commands after session/new
	n := acpNode(t, "session: thread")
	opts, err := n.AgentOptions(ctx, me, "fake")
	if err != nil || len(opts.Options) != 2 || opts.Options[0].Category != "model" || len(opts.Options[0].Values) != 2 || opts.Options[1].Category != "thought_level" {
		t.Fatalf("%+v %v", opts, err)
	}
	m := ask(t, n, SendRequest{Body: "settings", Model: "fake-big", Effort: "high"})
	if m.Reply != "model=fake-big effort=high" {
		t.Fatalf("settings %q", m.Reply)
	}
	// The next message without settings keeps the session's.
	if next := ask(t, n, SendRequest{Body: "settings", Thread: m.Thread}); next.Reply != "model=fake-big effort=high" {
		t.Fatalf("next %q", next.Reply)
	}
	bad := ask(t, n, SendRequest{Body: "settings", Model: "no-such-model"})
	if bad.State != store.Done {
		t.Fatalf("%+v", bad)
	}
	evs, _ := n.Events(ctx, me, bad.ID, 0, 50)
	noted := false
	for _, e := range evs {
		noted = noted || e.Kind == "notice"
	}
	if !noted {
		t.Fatal("no notice of the model not offered")
	}
	if opts, _ := n.AgentOptions(ctx, me, "fake"); len(opts.Commands) == 0 || opts.Commands[0].Name != "review" {
		t.Fatalf("commands %+v", opts.Commands)
	}
}

// A session started elsewhere is replayed as events, the user's messages among them; nothing is
// stored; a message continues it afterwards.
func TestSessionHistory(t *testing.T) {
	n := acpNode(t, "")
	evs, err := n.SessionHistory(ctx, me, "fake", "hist-1", "")
	if err != nil {
		t.Fatal(err)
	}
	var kinds []string
	for _, e := range evs {
		kinds = append(kinds, e.Kind)
	}
	got := strings.Join(kinds, " ")
	if got != "user thought tool_call tool_call_update text user text" {
		t.Fatalf("history %s", got)
	}
	if !strings.Contains(evs[0].Data, "config.go") || !strings.Contains(evs[4].Data, "YAML") {
		t.Fatalf("%+v", evs)
	}
	if _, err := n.SessionHistory(ctx, Principal{Kind: "client", Name: "ci"}, "fake", "hist-1", ""); code(err) != "refused" {
		t.Fatalf("a client read a session: %v", err)
	}
	if _, err := n.SessionHistory(ctx, me, "fake", "unknown", ""); err == nil {
		t.Fatal("an unknown session replayed")
	}
	// Continue it.
	if m := ask(t, n, SendRequest{Body: "echo more", Session: "hist-1"}); m.State != store.Done || m.Reply != "more" {
		t.Fatalf("%+v", m)
	}
}
