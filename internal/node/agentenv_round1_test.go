package node

import (
	"fmt"
	"os"
	"testing"
)

// The agent gets the node's environment without the node's secrets: a variable a token_env
// names (whatever it is called), the same token copied under another name, SANDBOXD_*/HUB_*;
// ordinary variables, PATH and the agent's own env still reach it.
func TestAgentEnvKeepsNodeSecrets(t *testing.T) {
	t.Setenv("CI_SECRET_X", "client-token-value-1")
	t.Setenv("COPY_OF_IT", "client-token-value-1")
	t.Setenv("SANDBOXD_URL", "https://sandboxd.example")
	t.Setenv("HUB_ANYTHING", "x")
	t.Setenv("PLAIN_SETTING", "kept")
	n := newNode(t, fmt.Sprintf(`
tokens:
  - {name: ci, token_env: CI_SECRET_X, send_to: ["*"]}
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", OWN: "mine"}
    preamble: false
`, os.Args[0]))
	for name, want := range map[string]string{
		"CI_SECRET_X": "", "COPY_OF_IT": "", "SANDBOXD_URL": "", "HUB_ANYTHING": "",
		"PLAIN_SETTING": "kept", "OWN": "mine", "PATH": os.Getenv("PATH"),
	} {
		if w := ask(t, n, SendRequest{Body: "env " + name}); w.Reply != want {
			t.Errorf("agent sees %s=%q, want %q", name, w.Reply, want)
		}
	}
}
