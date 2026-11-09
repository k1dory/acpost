package node

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/k1dory/acpost/internal/store"
)

// R1: an agent process inherits every variable of the node's environment except ACPOST_*,
// including the ones token_env points at.
func TestReviewAgentInheritsNodeSecrets(t *testing.T) {
	t.Setenv("HUB_JOIN_TOKEN", "hub-secret-value")
	t.Setenv("SANDBOXD_TOKEN", "sandbox-secret-value")
	n := acpNode(t, "preamble: false")
	if w := ask(t, n, SendRequest{Body: "env HUB_JOIN_TOKEN"}); w.Reply != "" {
		t.Errorf("agent sees HUB_JOIN_TOKEN=%q", w.Reply)
	}
	if w := ask(t, n, SendRequest{Body: "env SANDBOXD_TOKEN"}); w.Reply != "" {
		t.Errorf("agent sees SANDBOXD_TOKEN=%q", w.Reply)
	}
}

// R2: the lock of a task's first turn is "task:T"; a message arriving once the session is stored
// locks "sid:X" instead, so two turns of one task run at the same time with concurrency > 1.
func TestReviewTaskLockFlips(t *testing.T) {
	n := acpNode(t, "preamble: false\nconcurrency: 3")
	start := time.Now()
	a, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 800", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	// wait until the session of task T is stored (the first turn is inside its prompt)
	deadline := time.Now().Add(5 * time.Second)
	for {
		if sid, _ := n.st.Session(ctx, "agent:fake", "task:T"); sid != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no session stored")
		}
		time.Sleep(5 * time.Millisecond)
	}
	b := ask(t, n, SendRequest{Body: "sleep 100", Task: "T"})
	bDone := time.Since(start)
	wa, _ := n.Wait(ctx, me, a.ID, 10*time.Second)
	aDone := time.Since(start)
	t.Logf("A(sleep 800) done at %v state=%s; B(sleep 100, same task) done at %v state=%s", aDone, wa.State, bDone, b.State)
	if bDone < 800*time.Millisecond {
		t.Errorf("B of task T finished at %v, before A's turn of the same task ended: two turns of one task ran at once", bDone)
	}
}

// R3: session/new that never answers is not bounded by turn_timeout: the message stays running.
func TestReviewHangingNewSessionNotTimedOut(t *testing.T) {
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_HANG_NEW: "1"}
    preamble: false
    turn_timeout: 300ms
`, os.Args[0]))
	m, _, _ := n.Send(ctx, me, SendRequest{To: "fake", Body: "echo hi"})
	w, _ := n.Wait(ctx, me, m.ID, 3*time.Second)
	t.Logf("after 3s with turn_timeout 300ms: state=%s code=%q err=%q", w.State, w.ErrorCode, w.Error)
	if !w.State.Final() {
		t.Errorf("turn_timeout does not cover session/new: still %s after 3s", w.State)
	}
}

// R4: a stored session that fails to load in another slot's process silently becomes a new
// session, and the first slot keeps using the old one: two sessions for one task key.
func TestReviewLoadFailureSplitsSession(t *testing.T) {
	state := t.TempDir()
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_STATE: %q, FAKE_EXCLUSIVE: "1"}
    preamble: false
    concurrency: 2
`, os.Args[0], state))
	a := n.acp["fake"]
	s0, s1 := a.slots[0], a.slots[1]
	p0, err := s0.ensureProc(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sid0, err := s0.session(ctx, p0, "task:T")
	if err != nil {
		t.Fatal(err)
	}
	// slot 0 prompts once: the fake's lock file names slot 0's pid
	tr := p0.cl.begin(sid0, "x", 1024, 1, 1024)
	if _, err := p0.conn.Prompt(ctx, acp.PromptRequest{SessionId: sid0, Prompt: textBlocks("count")}); err != nil {
		t.Fatal(err)
	}
	p0.cl.end(sid0)
	_ = tr
	p1, err := s1.ensureProc(ctx)
	if err != nil {
		t.Fatal(err)
	}
	sid1, err := s1.session(ctx, p1, "task:T")
	t.Logf("slot0 sid=%s; slot1 sid=%s err=%v", sid0, sid1, err)
	if err == nil && sid1 != sid0 {
		t.Errorf("slot 1 silently opened a NEW session %s for task T (stored %s): the task's context is lost", sid1, sid0)
	}
	stored, _ := n.st.Session(ctx, "agent:fake", "task:T")
	// slot 0's process was stopped to free the session for slot 1: slot 0 starts again and takes the
	// same session back the same way.
	if p0, err = s0.ensureProc(ctx); err != nil {
		t.Fatal(err)
	}
	sid0again, _ := s0.session(ctx, p0, "task:T")
	t.Logf("stored now=%s; slot0 again=%s", stored, sid0again)
	if string(sid0again) != stored {
		t.Errorf("split brain: slot 0 uses %s while the store says %s", sid0again, stored)
	}
}

// R5: git processes per turn in a repository.
func TestReviewGitProcessesPerTurn(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip()
	}
	repo := testRepo(t)
	log := filepath.Join(t.TempDir(), "git.log")
	bin := t.TempDir()
	real, _ := exec.LookPath("git")
	os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"$*\" >> "+log+"\nexec "+real+" \"$@\"\n"), 0o755)
	t.Setenv("PATH", bin+":"+os.Getenv("PATH"))
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1"}
    preamble: false
    cwd: %q
`, os.Args[0], repo))
	ask(t, n, SendRequest{Body: "echo one"})
	b, _ := os.ReadFile(log)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	t.Logf("%d git processes for one turn without changes:\n%s", len(lines), string(b))
	os.WriteFile(filepath.Join(repo, "a.txt"), []byte("changed\n"), 0o644)
	os.Remove(log)
	ask(t, n, SendRequest{Body: "echo two"})
	b, _ = os.ReadFile(log)
	t.Logf("%d git processes for one turn with a change", len(strings.Split(strings.TrimSpace(string(b)), "\n")))
}

var _ = store.Done
