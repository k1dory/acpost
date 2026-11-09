package node

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// A5: a turn in a repository runs a bounded number of git processes: the filter drivers are read
// once a snapshot (not once a git call), the repository and its index in one rev-parse. 24 before.
func TestGitProcessesPerTurnBounded(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	repo := testRepo(t)
	log := filepath.Join(t.TempDir(), "git.log")
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\necho \"$*\" >> "+log+"\nexec "+real+" \"$@\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
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
	count := func() int {
		b, _ := os.ReadFile(log)
		os.Remove(log)
		return len(strings.Split(strings.TrimSpace(string(b)), "\n"))
	}
	ask(t, n, SendRequest{Body: "echo one"})
	if got := count(); got > 12 {
		t.Errorf("%d git processes for a turn that changed nothing, want at most 12", got)
	}
	w := ask(t, n, SendRequest{Body: "shell echo changed > a.txt"})
	if got := count(); got > 16 {
		t.Errorf("%d git processes for a turn with a change, want at most 16", got)
	}
	evs, err := n.st.Events(ctx, w.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		found = found || e.Kind == "changes" && strings.Contains(e.Data, "a.txt")
	}
	if !found {
		t.Error("the change was not told")
	}
}

// git's stderr is kept up to a bound, whatever git writes.
func TestCappedKeepsTheStart(t *testing.T) {
	c := &capped{max: 4}
	if n, err := c.Write([]byte("abcdef")); n != 6 || err != nil {
		t.Fatal(n, err)
	}
	c.Write([]byte("gh"))
	if c.String() != "abcd" {
		t.Fatalf("%q", c.String())
	}
}
