package store

import (
	"testing"
	"time"
)

// A chat's directory (review round 6): an artifact's own message's, else its thread's root's, else
// the first kept message's that says one — for a page of threads at once.
func TestChatDirsPrecedence(t *testing.T) {
	s := openTest(t, Options{})
	put := func(thread string, meta map[string]string) string {
		t.Helper()
		m := msg("agent:a", "x")
		m.Thread, m.Meta = thread, meta
		mustInsert(t, s, m)
		return m.ID
	}
	// A: the root says one; a later message another.
	a := put("", map[string]string{"acp_cwd": "/root-a"})
	ownA := put(a, map[string]string{"acp_session_cwd": "/own", "acp_cwd": "/asked"})
	put(a, map[string]string{"acp_cwd": "/later-a"})
	// B: the root says none; the first message that does wins over a later one.
	b := put("", nil)
	put(b, map[string]string{"model": "x"})
	put(b, map[string]string{"acp_cwd": "/first-b"})
	put(b, map[string]string{"acp_cwd": "/second-b"})
	// C: nothing anywhere.
	c := put("", nil)
	put(c, nil)

	byMsg, byThread, err := s.ChatDirs(ctx, []string{ownA, b}, []string{a, b, c, "gone"})
	if err != nil {
		t.Fatal(err)
	}
	if byMsg[ownA] != "/own" || byMsg[b] != "" {
		t.Errorf("by message %v", byMsg)
	}
	if byThread[a] != "/root-a" || byThread[b] != "/first-b" || byThread[c] != "" || byThread["gone"] != "" {
		t.Errorf("by thread %v", byThread)
	}
}

// Long threads with no directory anywhere: one query each way, not a walk of every message.
func TestChatDirsLongThreads(t *testing.T) {
	s := openTest(t, Options{})
	var threads []string
	for range 3 {
		root := msg("agent:a", "start")
		mustInsert(t, s, root)
		threads = append(threads, root.ID)
		for range 1500 {
			m := msg("agent:a", "a body that is not read")
			m.Thread = root.ID
			mustInsert(t, s, m)
		}
	}
	start := time.Now()
	_, byThread, err := s.ChatDirs(ctx, threads, threads)
	if err != nil || len(byThread) != 0 {
		t.Fatalf("%v %v", byThread, err)
	}
	if took := time.Since(start); took > 500*time.Millisecond {
		t.Fatalf("took %v", took)
	}
}
