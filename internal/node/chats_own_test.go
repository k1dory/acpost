package node

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

// cancelledTurn is a message whose turn was cancelled while it ran: the kind that leaves a session.
func cancelledTurn(t *testing.T, n *Node) store.Message {
	t.Helper()
	m, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 60000"})
	if err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(20 * time.Second); ; time.Sleep(20 * time.Millisecond) {
		if got, err := n.st.Get(ctx, m.ID); err == nil && got.State == store.Running {
			break
		}
		if time.Now().After(end) {
			t.Fatal("the turn did not start")
		}
	}
	// Its session is kept under its key by now (where the agent keeps one: not session: message),
	// as when a turn is cancelled while the agent works.
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if sid, _ := n.st.Session(ctx, "agent:fake", "thread:"+m.Thread); sid != "" {
			break
		}
	}
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	w, err := n.Wait(ctx, me, m.ID, 20*time.Second)
	if err != nil || w.State != store.Cancelled {
		t.Fatalf("not cancelled: %v %v", w.State, err)
	}
	return w
}

// Listing an agent's sessions for the chats starts one process, however many pages the agent
// gives them in (a spare process was started for each page: codex-acp, every few seconds).
func TestSessionListingStartsOneProcess(t *testing.T) {
	state, starts := t.TempDir(), filepath.Join(t.TempDir(), "starts")
	for i := range 5 {
		os.WriteFile(filepath.Join(state, fmt.Sprintf("outside-%d", i)), []byte("1"), 0o600)
	}
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q, FAKE_PAGE: "2", FAKE_STARTS: %q}
`, os.Args[0], state, starts))
	c, err := n.Chats(ctx, me)
	if err != nil || len(c.Items) != 5 || len(c.Partial) != 0 {
		t.Fatalf("chats: %d, %v, %v", len(c.Items), c.Partial, err)
	}
	b, _ := os.ReadFile(starts)
	if got := len(strings.Fields(string(b))); got != 1 {
		t.Errorf("%d processes started for one listing of three pages, want 1", got)
	}
}

// What the app takes off a prompt read back (ownBody in the desktop's turn.ts) is what the node
// puts there, to the letter: a change here is made there too.
func TestOwnPromptTextIsTheDesktops(t *testing.T) {
	ts, err := os.ReadFile("../../desktop/frontend/src/lib/turn.ts")
	if err != nil {
		t.Fatal(err)
	}
	s := &acpSlot{a: &acpAgent{cfg: &config.Agent{}}}
	head := s.prompt(store.Message{ID: "msg_X", From: "user:u"}, nil)
	_, tail, _ := strings.Cut(head, ".\n")
	for name, text := range map[string]string{"the header's lines": tail, "the reader's note": readerNote} {
		var b strings.Builder
		e := json.NewEncoder(&b)
		e.SetEscapeHTML(false)
		e.Encode(text)
		if !strings.Contains(string(ts), strings.TrimSpace(b.String())) {
			t.Errorf("turn.ts does not have %s as the node writes it: %s", name, b.String())
		}
	}
}

// Review round 4. A session for one message (session: message) is no thread's: it stays among
// the chats, where it is forked from.
func TestOneOffSessionsStayListed(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    session: message
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
`, os.Args[0], state))
	ask(t, n, SendRequest{Body: "echo hi"})
	entries, _ := os.ReadDir(state)
	if len(entries) != 1 {
		t.Fatalf("%d sessions kept by the fake agent", len(entries))
	}
	// A cancelled message's session, named after the node's prompt: its own too.
	gone := cancelledTurn(t, n)
	os.WriteFile(filepath.Join(state, "oneoff-cancelled"), []byte("title:[acpost] Message "+gone.ID+" from user:tester."), 0o600)
	c, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	found, cancelled := false, false
	for _, it := range c.Items {
		found = found || it.Key == "session:fake/"+entries[0].Name()
		cancelled = cancelled || it.Key == "session:fake/oneoff-cancelled"
	}
	if !found || !cancelled {
		t.Errorf("a message's own session is not among the chats (ended well: %v, cancelled: %v)", found, cancelled)
	}
}

// Review round 5. A session some key still runs is not a left one: a thread whose first message
// had a task and whose next has none (as the desktop sends them) runs two sessions, and the one
// the thread's chat does not name stays among the chats.
func TestSessionInUseStaysListed(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    session: task
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
`, os.Args[0], state))
	root := ask(t, n, SendRequest{Body: "echo one", Task: "T"})
	ask(t, n, SendRequest{Body: "echo two", Thread: root.ID})
	entries, _ := os.ReadDir(state)
	if len(entries) != 2 {
		t.Fatalf("%d sessions: the two messages were to run one each", len(entries))
	}
	c, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	shown := map[string]bool{}
	for _, it := range c.Items {
		if it.Session != "" {
			shown[it.Session] = true
		}
	}
	for _, e := range entries {
		if !shown[e.Name()] {
			t.Errorf("the session %s, which the thread runs, is nowhere among the chats", e.Name())
		}
	}
}

// Review round 6. A session of a message alone stays a chat when the agent's session mode is
// changed afterwards: one marked as such, and one made before the marks (no key was ever kept for
// its thread, so it is not taken for a thread's left session).
func TestOneOffSessionsSurviveAModeChange(t *testing.T) {
	dir := t.TempDir()
	os.Chmod(dir, 0o700)
	state, db := t.TempDir(), filepath.Join(dir, "store.db")
	conf := func(mode string) string {
		return fmt.Sprintf(`
store: %q
agents:
  fake:
    driver: acp
    command: [%q]
    session: %s
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
`, "sqlite://"+db, os.Args[0], mode, state)
	}
	n, st := fileNode(t, conf("message"))
	gone := cancelledTurn(t, n)
	title := "title:[acpost] Message " + gone.ID + " from user:tester."
	entries, _ := os.ReadDir(state)
	if len(entries) != 1 {
		t.Fatalf("%d sessions kept by the fake agent", len(entries))
	}
	marked := entries[0].Name()
	os.WriteFile(filepath.Join(state, marked), []byte(title), 0o600)
	os.WriteFile(filepath.Join(state, "before-marks"), []byte(title), 0o600)
	n.Close()
	st.Close()

	n, _ = fileNode(t, conf("thread"))
	// The thread goes on under the new mode (a key is kept for it now): the first message's own
	// session is still no thread's left one.
	ask(t, n, SendRequest{Body: "echo on", Thread: gone.Thread})
	c, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	shown := map[string]bool{}
	for _, it := range c.Items {
		shown[it.Key] = true
	}
	if !shown["session:fake/"+marked] || !shown["session:fake/before-marks"] {
		t.Errorf("after the mode changed: marked %v, made before the marks %v", shown["session:fake/"+marked], shown["session:fake/before-marks"])
	}
}

// A turn cancelled before the node kept its session under the thread's key (the agent had made
// it already): the session is marked as made at once, so it is not a chat beside the thread.
func TestSessionOfATurnCancelledAtOnce(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q, FAKE_NEW_LOG: %q, FAKE_SLOW_SET: "700"}
    model: fake-big
`, os.Args[0], state, filepath.Join(t.TempDir(), "new.log")))
	m, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "echo hi"})
	if err != nil {
		t.Fatal(err)
	}
	// The agent has the session; the node is still setting it up (a slow mode change).
	for end := time.Now().Add(20 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if entries, _ := os.ReadDir(state); len(entries) > 0 {
			break
		}
		if time.Now().After(end) {
			t.Fatal("no session made")
		}
	}
	if _, err := n.Cancel(ctx, me, m.ID); err != nil {
		t.Fatal(err)
	}
	n.Wait(ctx, me, m.ID, 20*time.Second)
	if sid, _ := n.st.Session(ctx, "agent:fake", "thread:"+m.Thread); sid != "" {
		t.Fatal("the session was kept under its key before the cancel: the slow setup did not hold the turn")
	}
	// The turn's process is gone or idle by now; a session started elsewhere is there to be listed.
	time.Sleep(300 * time.Millisecond)
	os.WriteFile(filepath.Join(state, "elsewhere"), []byte("title:a session of the editor"), 0o600)
	c, err := n.Chats(ctx, me)
	if err != nil || len(c.Partial) != 0 {
		t.Fatalf("chats: %v, %v", c.Partial, err)
	}
	elsewhere := false
	for _, it := range c.Items {
		if it.Key == "session:fake/elsewhere" {
			elsewhere = true
		} else if it.Kind == "session" {
			t.Errorf("the cancelled turn's session is a chat of its own: %s", it.Key)
		}
	}
	if !elsewhere {
		t.Error("the sessions were not listed at all")
	}
}

// Keys are found under their prefix whatever follows it (a task named with an emoji).
func TestSessionsMarkedTakesAnyKey(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	for key, sid := range map[string]string{"task:base": "s1", "task:🔧 fix": "s2", "task:\U0010ffff": "s3", "taskx:": "no", "tas": "no2"} {
		if err := n.st.PutSession(ctx, "agent:a", key, sid); err != nil {
			t.Fatal(err)
		}
	}
	got, err := n.st.SessionsMarked(ctx, "agent:a", "task:")
	if err != nil || len(got) != 3 || !got["s1"] || !got["s2"] || !got["s3"] {
		t.Errorf("under task: %v, %v", got, err)
	}
}

// A session the node made for a thread, that the thread no longer runs (it went on in another), is
// no chat beside it, and is not one of the sessions the agent's limit counts. A session the node
// did not make stays, whatever it is named: one named after the node's own prompt (a fork starts
// with the prompt of the session it was made of), and a fork made here.
func TestMadeSessionLeftIsNotShown(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q, FAKE_PAGE: "100"}
`, os.Args[0], state))
	m := ask(t, n, SendRequest{Body: "echo hi"})
	first, _ := n.st.Session(ctx, "agent:fake", "thread:"+m.Thread)
	if _, err := os.Stat(filepath.Join(state, first)); first == "" || err != nil {
		t.Fatalf("the thread's session %q is not the fake agent's: %v", first, err)
	}
	if err := n.st.PutSession(ctx, "agent:fake", "thread:"+m.Thread, "another"); err != nil {
		t.Fatal(err)
	}
	title := []byte("title:[acpost] Message " + m.ID + " from user:tester.")
	for i := range chatSessionsPerAgent { // as many left ones as the limit
		id := fmt.Sprintf("left-%03d", i)
		os.WriteFile(filepath.Join(state, id), title, 0o600)
		if err := n.st.PutSession(ctx, "agent:fake", madeSession+id, id); err != nil {
			t.Fatal(err)
		}
	}
	fork, err := n.ForkSession(ctx, me, "fake", "hist-1", "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(state, fork), title, 0o600)
	os.WriteFile(filepath.Join(state, "same-title"), title, 0o600)
	old := filepath.Join(state, "elsewhere")
	os.WriteFile(old, []byte("title:a session of the editor"), 0o600)
	long := time.Now().Add(-time.Hour)
	os.Chtimes(old, long, long) // older than a limit's worth of left ones
	c, err := n.Chats(ctx, me)
	if err != nil || len(c.Partial) != 0 {
		t.Fatalf("chats: %v, %v", c.Partial, err)
	}
	shown := map[string]bool{}
	for _, it := range c.Items {
		shown[it.Key] = true
		if strings.HasPrefix(it.Key, "session:fake/left-") {
			t.Fatalf("listed %s", it.Key)
		}
	}
	if shown["session:fake/"+first] {
		t.Error("the session the thread left is a chat beside it")
	}
	for _, id := range []string{fork, "same-title", "elsewhere"} {
		if !shown["session:fake/"+id] {
			t.Errorf("%s is not among the chats", id)
		}
	}
}

// A session in use keeps its mark of having been made here past retention (the mark is renewed
// with each use, as the key is), so when its key moves on it is still known for a left one.
func TestMadeMarkIsKeptWhileTheSessionIsUsed(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
`, os.Args[0], state))
	m := ask(t, n, SendRequest{Body: "echo one"})
	sid, _ := n.st.Session(ctx, "agent:fake", "thread:"+m.Thread)
	time.Sleep(30 * time.Millisecond)
	between := time.Now()
	time.Sleep(30 * time.Millisecond)
	ask(t, n, SendRequest{Body: "echo two", Thread: m.Thread}) // used again
	if _, err := n.st.GC(ctx, between); err != nil {           // what was unused since goes
		t.Fatal(err)
	}
	if got, _ := n.st.Session(ctx, "agent:fake", madeSession+sid); got != sid {
		t.Fatalf("the mark of %s went though the session was used: %q", sid, got)
	}
	if now, _ := n.st.Session(ctx, "agent:fake", "thread:"+m.Thread); now != sid {
		t.Fatalf("the thread's session is %q, was %q", now, sid)
	}
}
