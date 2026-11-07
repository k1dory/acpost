package node

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// The chats are acpost's threads and the agent's sessions started elsewhere, each once (a thread's
// own session is not listed again), with their folders.
func TestChats(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
`, os.Args[0], state))
	m := ask(t, n, SendRequest{Body: "echo hi", Task: "T1"}) // session: task (the default): its session is in FAKE_STATE now
	// A session another client started (a terminal's Claude Code).
	os.WriteFile(filepath.Join(state, "outside-1"), []byte("1"), 0o600)

	f, err := n.CreateFolder(ctx, me, "argus")
	if err != nil {
		t.Fatal(err)
	}
	if err := n.MoveChat(ctx, me, "session:fake/outside-1", f.ID); err != nil {
		t.Fatal(err)
	}
	chats, err := n.Chats(ctx, me)
	if err != nil {
		t.Fatal(err)
	}
	if len(chats.Items) != 2 || len(chats.Partial) != 0 {
		t.Fatalf("%+v", chats)
	}
	byKey := map[string]int{}
	for i, c := range chats.Items {
		byKey[c.Key] = i
	}
	th, ok1 := byKey["thread:"+m.Thread]
	out, ok2 := byKey["session:fake/outside-1"]
	if !ok1 || !ok2 {
		t.Fatalf("%+v", chats.Items)
	}
	if chats.Items[th].Session == "" || chats.Items[out].Folder != f.ID || chats.Items[out].Cwd != "/fake" || len(chats.Folders) != 1 {
		t.Fatalf("%+v", chats)
	}
	if _, err := n.Chats(ctx, Principal{Kind: "client", Name: "ci"}); code(err) != "refused" {
		t.Fatalf("a client listed the chats: %v", err)
	}
}

// A session put in a folder keeps it once a thread continues it.
func TestChatsFolderFollowsTheSession(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
`, os.Args[0], state))
	os.WriteFile(filepath.Join(state, "outside-2"), []byte("1"), 0o600)
	f, _ := n.CreateFolder(ctx, me, "argus")
	n.MoveChat(ctx, me, "session:fake/outside-2", f.ID)
	m := ask(t, n, SendRequest{Body: "echo go on", Session: "outside-2"})
	chats, _ := n.Chats(ctx, me)
	for _, c := range chats.Items {
		if c.Key == "session:fake/outside-2" {
			t.Fatalf("the continued session listed apart: %+v", chats.Items)
		}
		if c.Key == "thread:"+m.Thread && c.Folder != f.ID {
			t.Fatalf("the thread lost the folder: %+v", c)
		}
	}
	// Out of the folder: the thread, and the session it continues with it.
	if err := n.MoveChat(ctx, me, "thread:"+m.Thread, ""); err != nil {
		t.Fatal(err)
	}
	chats, _ = n.Chats(ctx, me)
	for _, c := range chats.Items {
		if c.Folder != "" {
			t.Fatalf("still in a folder: %+v", c)
		}
	}
}

// A move to a folder that is not there changes nothing: the thread keeps the folder it inherits.
func TestChatsFailedMoveKeepsTheFolder(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q}
`, os.Args[0], state))
	os.WriteFile(filepath.Join(state, "outside-3"), []byte("1"), 0o600)
	f, _ := n.CreateFolder(ctx, me, "argus")
	n.MoveChat(ctx, me, "session:fake/outside-3", f.ID)
	m := ask(t, n, SendRequest{Body: "echo go on", Session: "outside-3"})
	if err := n.MoveChat(ctx, me, "thread:"+m.Thread, "fld_missing"); code(err) != "not_found" {
		t.Fatalf("a missing folder: %v", err)
	}
	chats, _ := n.Chats(ctx, me)
	for _, c := range chats.Items {
		if c.Key == "thread:"+m.Thread && c.Folder != f.ID {
			t.Fatalf("the failed move took the folder: %+v", c)
		}
	}
}
