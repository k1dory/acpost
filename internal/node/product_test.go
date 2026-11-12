package node

import (
	"testing"
	"time"
)

func TestCommandProduct(t *testing.T) {
	for _, c := range []struct {
		cmd  []string
		want string
	}{
		{[]string{"/opt/homebrew/bin/claude-agent-acp"}, "claude-agent-acp"},
		{[]string{"codex-acp", "-c", "model=o3"}, "codex-acp"},
		{[]string{"npx", "-y", "@zed-industries/codex-acp@0.3.1"}, "codex-acp"},
		{[]string{"gemini", "--experimental-acp"}, "gemini"},
		{[]string{"bun", "x", "opencode", "acp"}, "opencode"},
		{[]string{"uv", "tool", "run", "kimi-cli", "--acp"}, "kimi-cli"},
		{[]string{`C:\Users\me\AppData\Roaming\npm\claude-agent-acp.cmd`}, "claude-agent-acp"},
		{[]string{"env", "-u", "X", "goose.exe", "acp"}, "goose"},
		{nil, ""},
		{[]string{"npx"}, ""},
	} {
		if got := commandProduct(c.cmd); got != c.want {
			t.Errorf("%q: %q, want %q", c.cmd, got, c.want)
		}
	}
}

// Before the agent's process starts, Agents names its command's program when it is a known one; once it started, what it
// said it is; and that stays after the process idles out. Listing agents starts no process.
func TestAgentsProduct(t *testing.T) {
	n := acpNode(t, "preamble: false\nidle_timeout: 100ms")
	product := func() string {
		for _, a := range n.Agents(ctx) {
			if a.Address == "agent:fake" {
				return a.Product
			}
		}
		t.Fatal("agent:fake not listed")
		return ""
	}
	if got := product(); got != "" { // the test binary is no known agent program
		t.Fatalf("before a start: %q", got)
	}
	if n.agentRunning("fake") {
		t.Fatal("listing the agents started its process")
	}
	ask(t, n, SendRequest{Body: "echo hi"})
	if got := product(); got != "fake-agent" {
		t.Fatalf("after a start: %q", got)
	}
	deadline := time.Now().Add(10 * time.Second)
	for n.agentRunning("fake") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n.agentRunning("fake") {
		t.Fatal("agent process still running after idle_timeout")
	}
	if got := product(); got != "fake-agent" {
		t.Fatalf("after idling out: %q", got)
	}
}
