package api

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/acptest"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
)

// An agent's command can carry a key (env NAME=value): /v1/agents names its program, and no part of
// the key reaches a client, a restricted one included (review round 1).
func TestAgentsProductHidesCommandSecrets(t *testing.T) {
	const secret = "sk-example-0123456789"
	home := t.TempDir()
	acptest.SetHome(t.Setenv, home)
	t.Setenv("XDG_CONFIG_HOME", home+"/.config")
	t.Setenv("ACPOST_SECRETS_FILE", "1")
	y := fmt.Sprintf(`node: {name: hub, listen: ["tcp://127.0.0.1:0"]}
agents:
  bot: {driver: inbox}
  coder: {driver: acp, command: [env, "OPENAI_API_KEY=%s", "OTHER=%s", codex-acp, --token, %s]}
  shell: {driver: acp, command: [sh, -c, "OPENAI_API_KEY=%s codex-acp"]}
tokens:
  - {name: ci, sha256: %s, send_to: ["agent:bot"]}
`, secret, secret, secret, secret, sha(clientSecret))
	cfg, err := config.Parse([]byte(y), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Store = "memory:"
	st, err := store.Open(cfg.Store, node.StoreOptions(cfg))
	if err != nil {
		t.Fatal(err)
	}
	n, err := node.New(cfg, st, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := n.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	tcp := httptest.NewServer(Handler(n, false, 1<<20))
	t.Cleanup(func() { tcp.Close(); n.Close(); st.Close() })

	code, body := call(t, tcp, "GET", "/v1/agents", "Bearer "+clientSecret, "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, part := range []string{secret, "sk-example", "0123456789", "OPENAI_API_KEY", "OTHER="} {
		if strings.Contains(body, part) {
			t.Fatalf("/v1/agents shows %q: %s", part, body)
		}
	}
	if !strings.Contains(body, `"product":"codex-acp"`) {
		t.Fatalf("the program is not named: %s", body)
	}
}
