package node

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// nodeCLI makes the node's command a file of the test's, with the state in a directory of its own.
func nodeCLI(t *testing.T) (cli, state string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("a link needs a privilege on Windows: the command's own directory is used there")
	}
	state = t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	cli = filepath.Join(t.TempDir(), "acpost-1.2.3")
	if err := os.WriteFile(cli, []byte("#!/bin/sh\necho ok\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := CLI
	CLI = cli
	cliDir.once, cliDir.dir = sync.Once{}, ""
	t.Cleanup(func() { CLI = old; cliDir.once, cliDir.dir = sync.Once{}, "" })
	return cli, state
}

// An agent told to run `acpost send` finds the node's own command, though no directory of the
// node's PATH has one (the app; `acpost serve` started by its full path).
func TestAgentFindsTheNodesCommand(t *testing.T) {
	cli, state := nodeCLI(t)
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    preamble: false
    env: {ACPOST_FAKE_AGENT: "1"}
`, os.Args[0]))
	m := ask(t, n, SendRequest{Body: "env PATH"})
	dir := filepath.Join(state, "acpost", "bin")
	first, rest, _ := strings.Cut(m.Reply, string(os.PathListSeparator))
	if first != dir || rest == "" {
		t.Fatalf("the agent's PATH is %q (%s): the command's directory %s is not first, before the node's own", m.Reply, m.Error, dir)
	}
	if to, err := os.Readlink(filepath.Join(dir, "acpost")); err != nil || to != cli {
		t.Fatalf("acpost there is %q (%v), not the node's command %s", to, err, cli)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("the directory holds %d entries: only the command belongs first on a PATH", len(ents))
	}
}

// The agent's own PATH (env in its configuration) keeps its directories, after the command's.
func TestAgentsOwnPathKeepsTheCommand(t *testing.T) {
	_, state := nodeCLI(t)
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    preamble: false
    env: {ACPOST_FAKE_AGENT: "1", PATH: "/opt/own/bin"}
`, os.Args[0]))
	want := filepath.Join(state, "acpost", "bin") + string(os.PathListSeparator) + "/opt/own/bin"
	if m := ask(t, n, SendRequest{Body: "env PATH"}); m.Reply != want {
		t.Fatalf("the agent's PATH is %q, not %q", m.Reply, want)
	}
}

// A command that moved (the app updated, another acpost serves) is the one linked; none, or one
// that is gone, leaves the PATH alone.
func TestCommandLinkFollowsTheCommand(t *testing.T) {
	cli, _ := nodeCLI(t)
	dir := filepath.Join(t.TempDir(), "bin")
	if got := linkCLI(cli, dir); got != dir {
		t.Fatalf("no link: %q", got)
	}
	other := cli + ".new"
	if err := os.WriteFile(other, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if got := linkCLI(other, dir); got != dir {
		t.Fatalf("no link to the new command: %q", got)
	}
	if to, _ := os.Readlink(filepath.Join(dir, "acpost")); to != other {
		t.Fatalf("the link still names %q", to)
	}
	if got := linkCLI(filepath.Join(dir, "gone"), dir); got != "" {
		t.Fatalf("a command that is not there was put on the PATH: %q", got)
	}
	if got := linkCLI("", dir); got != "" {
		t.Fatalf("no command, and a directory on the PATH: %q", got)
	}
	CLI = ""
	if env := withCLI([]string{"PATH=/bin"}); len(env) != 1 || env[0] != "PATH=/bin" {
		t.Fatalf("without a command the environment changed: %q", env)
	}
}
