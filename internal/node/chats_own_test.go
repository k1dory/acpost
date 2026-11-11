package node

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
