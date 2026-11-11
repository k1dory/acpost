package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/plat"
)

func TestDefaults(t *testing.T) {
	c, err := Parse([]byte(""), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	state := plat.StateDir()
	if c.Store != "sqlite://"+filepath.Join(state, "store.db") {
		t.Fatalf("store %q", c.Store)
	}
	if len(c.Node.Listen) != 1 || !strings.HasPrefix(c.Node.Listen[0], "unix://"+state) {
		t.Fatalf("listen %v", c.Node.Listen)
	}
	if c.Retention.D() != 168*time.Hour || c.Limits.MaxBody != 1<<20 || c.Limits.MaxDepth != 4 ||
		c.Limits.MaxAttempts != 3 || c.Limits.MaxStore != 2<<30 || c.Limits.MaxQueuedPerAgent != 1000 {
		t.Fatalf("defaults %+v %+v", c.Retention, c.Limits)
	}
	if c.Node.Name == "" {
		t.Fatal("no node name")
	}
}

func TestParseYAML(t *testing.T) {
	c, err := Parse([]byte(`
node:
  name: laptop
  listen: [unix:///tmp/a.sock, tcp://127.0.0.1:7420]
store: memory
retention: 2h
limits: {max_body: 64KiB, max_store: 1GiB, max_depth: 2}
tokens:
  - name: ci
    sha256: 6b86b273ff34fce19d6b804eff5a3f5747ada4eaa22f1d49c01e52ddb7875b4b
    send_to: ["agent:reviewer"]
agents:
  reviewer:
    driver: acp
    command: [codex-acp, --flag]
    cwd: ~/src
    env: {A: b}
    permissions: deny
    session: thread
    turn_timeout: 5m
  bot:
    driver: inbox
    lease: 1m
`), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	r := c.Agents["reviewer"]
	if r.Driver != "acp" || r.Command[1] != "--flag" || r.Cwd != filepath.Join(home, "src") || r.Env["A"] != "b" ||
		r.Permissions != "deny" || r.Session != "thread" || r.TurnTimeout.D() != 5*time.Minute {
		t.Fatalf("reviewer %+v", r)
	}
	// agent defaults
	if r.Concurrency != 1 || r.Reply != "last" || r.IdleTimeout.D() != 10*time.Minute || !r.PreambleOn() || r.AllowFrom[0] != "*" {
		t.Fatalf("agent defaults %+v", r)
	}
	if b := c.Agents["bot"]; b.Lease.D() != time.Minute {
		t.Fatalf("bot %+v", b)
	}
	if c.Limits.MaxBody != 64<<10 || c.Limits.MaxStore != 1<<30 || c.Limits.MaxDepth != 2 || c.Retention.D() != 2*time.Hour {
		t.Fatalf("limits %+v", c.Limits)
	}
	if c.Tokens[0].SendTo[0] != "agent:reviewer" {
		t.Fatal(c.Tokens)
	}
}

func TestParseJSON(t *testing.T) {
	c, err := Parse([]byte(`{"agents":{"r":{"driver":"acp","command":["x"],"turn_timeout":"90s"}},"limits":{"max_reply":"2MiB"}}`), ".json")
	if err != nil {
		t.Fatal(err)
	}
	if c.Agents["r"].TurnTimeout.D() != 90*time.Second || c.Limits.MaxReply != 2<<20 {
		t.Fatalf("%+v", c)
	}
}

func TestInvalid(t *testing.T) {
	for name, tc := range map[string]struct{ src, want string }{
		"unknown field":     {"agnets: {}", "agnets"},
		"unknown driver":    {"agents: {a: {driver: rpc}}", "driver"},
		"acp no command":    {"agents: {a: {driver: acp}}", "command"},
		"bad permissions":   {"agents: {a: {driver: acp, command: [x], permissions: maybe}}", "permissions"},
		"bad session":       {"agents: {a: {driver: acp, command: [x], session: forever}}", "session"},
		"bad name":          {"agents: {\"a b\": {driver: inbox}}", "name"},
		"bad size":          {"limits: {max_body: lots}", "lots"},
		"bad duration":      {"retention: soon", "soon"},
		"open tcp no tls":   {"node: {listen: [tcp://0.0.0.0:7420]}", "tls"},
		"bad listen":        {"node: {listen: [udp://x]}", "listen"},
		"token without sha": {"tokens: [{name: x}]", "sha256"},
		"bad store":         {"store: mysql://x", "store"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src), ".yaml")
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %v, want mention of %q", err, tc.want)
			}
		})
	}
}

func TestLoopbackTCPWithoutTLSIsFine(t *testing.T) {
	if _, err := Parse([]byte("node: {listen: [tcp://127.0.0.1:7420]}"), ".yaml"); err != nil {
		t.Fatal(err)
	}
}

func TestTokenFromEnv(t *testing.T) {
	t.Setenv("CI_TOKEN", "secret")
	c, err := Parse([]byte("tokens: [{name: ci, token_env: CI_TOKEN}]"), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !c.Tokens[0].Matches("secret") || c.Tokens[0].Matches("other") {
		t.Fatal("token match")
	}
	if _, err := Parse([]byte("tokens: [{name: ci, token_env: NOPE_UNSET}]"), ".yaml"); err == nil {
		t.Fatal("unset token env accepted")
	}
}

func TestLoadMissingFileIsDefaults(t *testing.T) {
	t.Setenv("ACPOST_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	c, err := Load(DefaultPath())
	if err != nil || c.Limits.MaxBody == 0 {
		t.Fatal(err)
	}
}

func TestSizes(t *testing.T) {
	for in, want := range map[string]int64{"10": 10, "1KiB": 1024, "2MiB": 2 << 20, "1GiB": 1 << 30, "5KB": 5000, "3MB": 3e6} {
		var s Size
		if err := s.parse(in); err != nil || int64(s) != want {
			t.Fatalf("%s: %d %v", in, s, err)
		}
	}
}

// The CLI reads the node's file without the hub's token: only the node, joining, needs it.
func TestJoinTokenNeededOnlyByTheNode(t *testing.T) {
	t.Setenv("ACPOST_TEST_UNSET_TOKEN", "")
	c, err := Parse([]byte("node: {join: {url: https://hub:7420, token_env: ACPOST_TEST_UNSET_TOKEN}}\n"), ".yaml")
	if err != nil {
		t.Fatalf("a CLI without the token cannot read the config: %v", err)
	}
	if c.Node.Join.Token != "" {
		t.Fatal("token from nowhere")
	}
	if _, err := Parse([]byte("node: {join: {url: https://hub:7420}}\n"), ".yaml"); err == nil {
		t.Fatal("join without any token accepted")
	}
}
