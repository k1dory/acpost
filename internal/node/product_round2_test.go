package node

import (
	"strings"
	"testing"
)

// A command's product is only ever a name from the table of known agent programs: every entry is
// found behind the launchers, and nothing else in a command (an option's value, an assignment
// that holds a known name, an unknown program) comes out.
func TestCommandProductOnlyKnownPrograms(t *testing.T) {
	for name, want := range knownPrograms {
		for _, cmd := range [][]string{
			{name},
			{"/usr/local/bin/" + name, "--acp"},
			{`C:\tools\` + strings.ToUpper(name) + ".exe"},
			{"env", "K=v", name},
			{"npx", "-y", "@scope/" + name + "@1.2.3", "--acp"},
			{"uvx", "--from", "x", name},
			{"node", "--require", "x", "/opt/" + name + "/dist/" + name + ".js"},
			{"python3", "-m", name},
		} {
			if got := commandProduct(cmd); got != want {
				t.Errorf("%q: %q, want %q", cmd, got, want)
			}
		}
	}
	for _, cmd := range [][]string{
		{"npx", "--token", "sk-abc", "prog"},
		{"npx", "--token", "sk-abc"},
		{"env", "X=codex-acp", "true"},
		{"env", "X=codex-acp"},
		{"my-own-agent", "--acp"},
		{"npx", "--token", "codexx", "prog"},
		{"sh", "-c", "codex-acp"},
		{"node", "-e", "codex"},
	} {
		if got := commandProduct(cmd); got != "" {
			t.Errorf("%q: %q, want nothing", cmd, got)
		}
	}
}
