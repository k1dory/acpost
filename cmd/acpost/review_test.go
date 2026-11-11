package main

// Regression tests from the Codex review (gpt-6-astra, 2026-11-05).

import (
	"github.com/k1dory/acpost/internal/config"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestAutostartWithRelativeConfig(t *testing.T) {
	e := newEnv(t, inbox)
	fallback := filepath.Join(e.dir, "fallback-state")
	e.vars = append(e.vars, "XDG_STATE_HOME="+fallback)
	t.Chdir(e.dir)
	t.Cleanup(func() {
		for _, dir := range []string{
			filepath.Dir(e.sock), filepath.Join(fallback, "acpost"),
		} {
			b, err := os.ReadFile(filepath.Join(dir, "acpost.pid"))
			if err != nil {
				continue
			}
			pid, err := strconv.Atoi(strings.TrimSpace(string(b)))
			if err == nil && pid > 0 {
				askToStop(pid)
			}
		}
	})

	r := e.run("", "--config", "config.yaml", "send", "--to", "bot", "--body", "x")
	if r.code != 0 {
		t.Fatalf("relative config autostart: exit=%d stderr=%s", r.code, r.err)
	}
}

func TestLocalURLUsesConfiguredTLS(t *testing.T) {
	cfg, err := config.Parse([]byte(`
node:
  listen: [tcp://127.0.0.1:7420]
  tls: {cert: cert.pem, key: key.pem}
`), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	if got := localURL(cfg); got != "https://127.0.0.1:7420" {
		t.Fatalf("TLS node URL is %q", got)
	}
}

func TestBodyLimitAppliesBeforeJSONEscaping(t *testing.T) {
	e := newEnv(t, inbox)
	e.serve()
	r := e.run(strings.Repeat("<", 400000), "send", "--to", "bot")
	if r.code != 0 {
		t.Fatalf("valid 400000-byte body refused: exit=%d stderr=%s", r.code, r.err)
	}
}

func TestSessionsAgentFailureIsNotMissingNode(t *testing.T) {
	e := newEnv(t, `
agents:
  broken: {driver: acp, command: [/acpost-no-such-agent]}
`)
	e.serve()
	e.ok("", "status")

	r := e.run("", "sessions", "--agent", "broken", "--json")
	if r.code != exitError {
		t.Fatalf("agent failure: exit=%d, want %d; stderr=%s",
			r.code, exitError, r.err)
	}
}
