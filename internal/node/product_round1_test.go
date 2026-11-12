package node

import (
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

// The command's product is only ever a program's or a package's name: an env assignment (a key),
// an option's operand or an argument never shows in it, whatever the command (review round 1).
func TestCommandProductDisclosesNothing(t *testing.T) {
	const secret = "sk-example-secret"
	for _, c := range []struct {
		cmd  []string
		want string
	}{
		{[]string{"env", "OPENAI_API_KEY=" + secret, "codex-acp"}, "codex-acp"},
		{[]string{"env", "A=1", "OPENAI_API_KEY=" + secret, "B=" + secret, "/usr/local/bin/codex-acp", "--key", secret}, "codex-acp"},
		{[]string{"env", "-i", "A=" + secret, "prog"}, "prog"},
		{[]string{"env", "-u", "HOME", "-C", "/tmp", "K=" + secret, "--", "gemini", secret}, "gemini"},
		{[]string{"/usr/bin/env", "K=" + secret}, ""},
		{[]string{"npx", "-y", "--package", "x@1", "bin"}, "bin"},
		{[]string{"npx", "-y", "--package", secret}, ""},
		{[]string{"npx", "--registry", secret, "@zed-industries/claude-code-acp@latest"}, "claude-code-acp"},
		{[]string{"pnpm", "dlx", "--package", secret, "opencode"}, "opencode"},
		{[]string{"yarn", "dlx", "-p", secret, "qwen-code"}, "qwen-code"},
		{[]string{"uvx", "--from", secret, "kimi-cli"}, "kimi-cli"},
		{[]string{"uv", "tool", "run", "--with", secret, "aider"}, "aider"},
		{[]string{"pipx", "run", "--spec", secret, "aider"}, "aider"},
		{[]string{"node", "--require", secret, "/opt/agents/codex/dist/codex-acp.js", secret}, "codex-acp"},
		{[]string{"node", "-e", secret}, ""},
		{[]string{"python3", "-m", "aider", secret}, "aider"},
		{[]string{"python3", "-c", secret}, ""},
		{[]string{"sh", "-c", "OPENAI_API_KEY=" + secret + " codex-acp"}, ""},
		{[]string{"bash", "/opt/start-agent.sh"}, ""},
		{[]string{"A=" + secret, "B=" + secret}, ""},
		{[]string{"/Applications/My Agents/bin/goose", "acp"}, "goose"},
		{[]string{"/opt/My Agent"}, ""},
		{[]string{"agent " + secret}, ""},
		{[]string{strings.Repeat("a", 65)}, ""},
		{[]string{"tool\x00" + secret}, ""},
	} {
		got := commandProduct(c.cmd)
		if got != c.want {
			t.Errorf("%q: %q, want %q", c.cmd, got, c.want)
		}
		if strings.Contains(got, "secret") || strings.Contains(got, "=") || strings.ContainsAny(got, " \t") {
			t.Errorf("%q: product %q discloses an argument", c.cmd, got)
		}
	}
}

// What the agent says it is is its own text: kept only printable and at most 64 characters, the
// title in its place when the name is not.
func TestLearnProductCleansAgentInfo(t *testing.T) {
	title := "Codex"
	for _, c := range []struct {
		info *acp.Implementation
		want string
	}{
		{&acp.Implementation{Name: "  codex-acp  "}, "codex-acp"},
		{&acp.Implementation{Name: "codex\x1b[31m"}, ""},
		{&acp.Implementation{Name: strings.Repeat("x", 65), Title: &title}, "Codex"},
		{&acp.Implementation{Name: "bad\nname"}, ""},
		{nil, ""},
	} {
		a := &acpAgent{cfg: nil}
		a.learnProduct(acp.InitializeResponse{AgentInfo: c.info})
		got := ""
		if v := a.product.Load(); v != nil {
			got = *v
		}
		if got != c.want {
			t.Errorf("%+v: %q, want %q", c.info, got, c.want)
		}
	}
}
