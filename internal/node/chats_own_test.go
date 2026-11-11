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
	m := ask(t, n, SendRequest{Body: "echo hi"})
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
	m := ask(t, n, SendRequest{Body: "echo hi"})
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
	title := "[acpost] Message " + m.ID + " from user:tester."
	fork, err := n.ForkSession(ctx, me, "fake", "hist-1", "")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(state, fork), []byte("title:"+title), 0o600)
	c, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	for _, it := range c.Items {
		keys = append(keys, it.Key)
	}
	got := strings.Join(keys, " ")
	if strings.Contains(got, "session:fake/"+first) || !strings.Contains(got, "session:fake/"+fork) || len(keys) != 2 {
		t.Errorf("chats: %s (the thread's first session %s, the fork %s)", got, first, fork)
	}
	withFiles := "[acpost] Attached files:\n- a.png (image/png, 3 bytes): in this prompt\n- b.txt (text/plain, 9 bytes): /tmp/x/b.txt (a copy until this turn ends)\n\n" + title
	at := m.FinishedAt
	if !n.leftBehind(ctx, withFiles, at) || !n.leftBehind(ctx, title, at.Add(2*time.Second)) {
		t.Error("a title of this node's prompt is not known")
	}
	// Written since the message ended: someone's conversation (a fork made before forks were marked).
	if n.leftBehind(ctx, title, at.Add(time.Minute)) || n.leftBehind(ctx, title, time.Time{}) {
		t.Error("a session written after its message ended was taken for a left one")
	}
	for _, other := range []string{"[acpost] Attached files:\n- a.png\n\nmy own text " + title, "see " + title, "[acpost] Message msg_01AAAAAAAAAAAAAAAAAAAAAAAA from user:else."} {
		if n.leftBehind(ctx, other, at) {
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
	m := ask(t, n, SendRequest{Body: "echo hi"})
	title := []byte("title:[acpost] Message " + m.ID + " from user:tester.")
	write := func(name string, at time.Time) {
		os.WriteFile(filepath.Join(state, name), title, 0o600)
		os.Chtimes(filepath.Join(state, name), at, at)
	}
	write("left", m.FinishedAt)                   // the turn's own, abandoned
	write("named", m.FinishedAt)                  // the same, but the user gave it a name
	write("later", m.FinishedAt.Add(time.Minute)) // a fork, written since
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
	if strings.Contains(got, "session:fake/left") || !strings.Contains(got, "session:fake/named") || !strings.Contains(got, "session:fake/later") {
		t.Errorf("chats: %s", got)
	}
}
