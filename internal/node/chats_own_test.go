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

// A session an agent names after a prompt of this node (codex-acp: its first message), that the
// message's thread no longer runs, is not a chat beside the thread; one named after a message this
// node does not have is.
func TestSessionLeftBehindIsNotShown(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
`, os.Args[0], state))
	m := cancelledTurn(t, n)
	os.WriteFile(filepath.Join(state, "left"), []byte("title:[acpost] Message "+m.ID+" from user:tester.\nYour final answer"), 0o600)
	os.WriteFile(filepath.Join(state, "theirs"), []byte("title:[acpost] Message msg_01AAAAAAAAAAAAAAAAAAAAAAAA from user:else."), 0o600)
	c, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, it := range c.Items {
		keys = append(keys, it.Key)
	}
	got := strings.Join(keys, " ")
	if strings.Contains(got, "session:fake/left") || !strings.Contains(got, "session:fake/theirs") || !strings.Contains(got, "thread:"+m.Thread) || len(keys) != 2 {
		t.Errorf("chats: %s", got)
	}
}

// Sessions left behind are not among those an agent's limit counts: a session started elsewhere,
// older than a limit's worth of them, is still listed.
func TestSessionsLeftBehindDoNotUseTheLimit(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q, FAKE_PAGE: "100"}
`, os.Args[0], state))
	m := cancelledTurn(t, n)
	for i := range chatSessionsPerAgent {
		os.WriteFile(filepath.Join(state, fmt.Sprintf("left-%03d", i)), []byte("title:[acpost] Message "+m.ID+" from user:tester."), 0o600)
	}
	old := filepath.Join(state, "elsewhere")
	os.WriteFile(old, []byte("title:a session of the editor"), 0o600)
	long := time.Now().Add(-time.Hour)
	os.Chtimes(old, long, long)
	c, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, it := range c.Items {
		found = found || it.Key == "session:fake/elsewhere"
		if strings.HasPrefix(it.Key, "session:fake/left-") {
			t.Fatalf("listed %s", it.Key)
		}
	}
	if !found {
		t.Errorf("the older session is not listed: %d chats", len(c.Items))
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

// Review round 2. A session the node made is its thread's whatever its title (the node marks it),
// also once the thread went on in another; a fork, which starts with the same prompt, is a chat
// of its own; a title that starts with the files the prompt lists is known too.
func TestSessionsMadeHereAndForks(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
`, os.Args[0], state))
	m := ask(t, n, SendRequest{Body: "echo hi"})
	first, _ := n.st.Session(ctx, "agent:fake", "thread:"+m.Thread)
	if first == "" {
		t.Fatal("no session stored")
	}
	if _, err := os.Stat(filepath.Join(state, first)); err != nil {
		t.Fatalf("the fake agent does not list the session: %v", err)
	}
	// The thread goes on in another session (its first turn's was left): the first is still not a chat.
	if err := n.st.PutSession(ctx, "agent:fake", "thread:"+m.Thread, "another"); err != nil {
		t.Fatal(err)
	}
	// A fork starts with the prompt of the session it was made of: here a cancelled turn's, made
	// at once (so nothing but its mark tells it from the session that turn left).
	gone := cancelledTurn(t, n)
	title := "[acpost] Message " + gone.ID + " from user:tester."
	fork, err := n.ForkSession(ctx, me, "fake", "hist-1", "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(state, fork), []byte("title:"+title), 0o600)
	os.WriteFile(filepath.Join(state, "same-unmarked"), []byte("title:"+title), 0o600)
	c, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, it := range c.Items {
		keys = append(keys, it.Key)
	}
	got := strings.Join(keys, " ")
	if strings.Contains(got, "session:fake/"+first) || !strings.Contains(got, "session:fake/"+fork) || strings.Contains(got, "session:fake/same-unmarked") {
		t.Errorf("chats: %s (the thread's first session %s, the fork %s)", got, first, fork)
	}
	withFiles := "[acpost] Attached files:\n- a.png (image/png, 3 bytes): in this prompt\n- b.txt (text/plain, 9 bytes): /tmp/x/b.txt (a copy until this turn ends)\n\n" + title
	// A turn that ended well left no session: one with its title is someone's (a fork).
	if n.leftBehind(ctx, n.agentCfg("fake"), "[acpost] Message "+m.ID+" from user:tester.", m.FinishedAt) {
		t.Error("a session named after a message that ended well was taken for a left one")
	}
	c2 := cancelledTurn(t, n)
	title = "[acpost] Message " + c2.ID + " from user:tester."
	withFiles = "[acpost] Attached files:\n- a.png (image/png, 3 bytes): in this prompt\n- b.txt (text/plain, 9 bytes): /tmp/x/b.txt (a copy until this turn ends)\n\n" + title
	at := c2.FinishedAt
	if !n.leftBehind(ctx, n.agentCfg("fake"), withFiles, at) || !n.leftBehind(ctx, n.agentCfg("fake"), title, at.Add(2*time.Second)) {
		t.Error("a title of this node's prompt is not known")
	}
	// Written since the message ended: someone's conversation (a fork made before forks were marked).
	if n.leftBehind(ctx, n.agentCfg("fake"), title, at.Add(time.Minute)) || n.leftBehind(ctx, n.agentCfg("fake"), title, time.Time{}) {
		t.Error("a session written after its message ended was taken for a left one")
	}
	for _, other := range []string{"[acpost] Attached files:\n- a.png\n\nmy own text " + title, "see " + title, "[acpost] Message msg_01AAAAAAAAAAAAAAAAAAAAAAAA from user:else."} {
		if n.leftBehind(ctx, n.agentCfg("fake"), other, at) {
			t.Errorf("taken for this node's: %q", other)
		}
	}
}

// Review round 3. A session with this node's title that the user named, or that was written after
// the message ended (a fork made before forks were marked, or outside acpost), is listed.
func TestUnmarkedForksStayListed(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
`, os.Args[0], state))
	m := cancelledTurn(t, n)
	title := []byte("title:[acpost] Message " + m.ID + " from user:tester.")
	write := func(name string, at time.Time) {
		os.WriteFile(filepath.Join(state, name), title, 0o600)
		os.Chtimes(filepath.Join(state, name), at, at)
	}
	write("left", m.FinishedAt)                   // the turn's own, abandoned
	write("named", m.FinishedAt)                  // the same, but the user gave it a name
	write("later", m.FinishedAt.Add(time.Minute)) // a fork, written since
	// A fork made seconds after a turn that ended well: that turn left no session, so it is one.
	well := ask(t, n, SendRequest{Body: "echo hi"})
	os.WriteFile(filepath.Join(state, "soon"), []byte("title:[acpost] Message "+well.ID+" from user:tester."), 0o600)
	if err := n.st.SetChatTitle(ctx, "session:fake/named", "Fork: mine"); err != nil {
		t.Fatal(err)
	}
	c, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, it := range c.Items {
		keys = append(keys, it.Key)
	}
	got := strings.Join(keys, " ")
	if strings.Contains(got, "session:fake/left") || !strings.Contains(got, "session:fake/named") || !strings.Contains(got, "session:fake/later") || !strings.Contains(got, "session:fake/soon") {
		t.Errorf("chats: %s", got)
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
		t.Skipf("%d sessions: the two messages share one here", len(entries))
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
		t.Skip("the session was kept under its key before the cancel: not this case")
	}
	c, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range c.Items {
		if it.Kind == "session" {
			t.Errorf("the cancelled turn's session is a chat of its own: %s", it.Key)
		}
	}
}
