package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
)

func TestMain(m *testing.M) {
	if os.Getenv("ACPOST_MUTE_AGENT") == "1" {
		muteAgent()
	}
	os.Exit(m.Run())
}

// muteAgent is an ACP agent that answers initialize (it can list sessions) and nothing after.
func muteAgent() {
	sc := bufio.NewScanner(os.Stdin)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var req struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
		}
		if json.Unmarshal(sc.Bytes(), &req) != nil || req.Method != "initialize" {
			continue
		}
		fmt.Printf(`{"jsonrpc":"2.0","id":%s,"result":{"protocolVersion":1,"agentCapabilities":{"loadSession":true,"sessionCapabilities":{"list":{}}},"agentInfo":{"name":"mute","version":"1"}}}`+"\n", req.ID)
	}
	os.Exit(0)
}

// cutByTheStop runs calls against a node and stops it while they wait: each must come back as
// unavailable (ask again), whatever the layer below made of the cancellation.
func cutByTheStop(t *testing.T, cfg *config.Config, sock string, calls map[string]func(*client.Client) error) {
	t.Helper()
	stop := runServer(t, cfg)
	c, _ := client.New("unix://"+sock, "", "")
	type res struct {
		name string
		err  error
	}
	out := make(chan res, len(calls))
	for name, f := range calls {
		go func() { out <- res{name, f(c)} }()
	}
	time.Sleep(700 * time.Millisecond) // the calls wait in their handlers
	if err, _ := stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
	for range calls {
		select {
		case r := <-out:
			if !errors.Is(r.err, client.ErrUnavailable) {
				t.Errorf("%s cut by the stop: %v", r.name, r.err)
			}
		case <-time.After(10 * time.Second):
			t.Fatal("a call did not end with the stop")
		}
	}
}

// An ACP agent that does not answer the session list: the stop cancels the ACP request, which the
// SDK reports as the agent's (agent_error, 502); the API answers unavailable.
func TestRunStopDuringACPRequestIsUnavailable(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "s", "a.sock")
	y := fmt.Sprintf("node:\n  name: mute1\n  listen: [unix://%s]\nstore: sqlite://%s/s/store.db\n"+
		"agents:\n  mute:\n    driver: acp\n    command: [%q]\n    env: {ACPOST_MUTE_AGENT: \"1\"}\n", sock, dir, os.Args[0])
	cfg, err := config.Parse([]byte(y), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	cutByTheStop(t, cfg, sock, map[string]func(*client.Client) error{
		"send --session latest": func(c *client.Client) error {
			_, _, err := c.Send(context.Background(), client.SendRequest{To: "agent:mute", Body: "x", Session: "latest"}, "")
			return err
		},
		"sessions": func(c *client.Client) error {
			_, _, err := c.Sessions(context.Background(), "mute", "", "")
			return err
		},
	})
}

// A handoff whose git is cut by the stop: unavailable, not invalid ("no remote origin").
func TestRunStopDuringHandoffGitIsUnavailable(t *testing.T) {
	real, err := exec.LookPath("git")
	if err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	if out, err := exec.Command(real, "init", "-q", repo).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, out)
	}
	bin := t.TempDir()
	script := "#!/bin/sh\ncase \"$*\" in *\"remote get-url\"*) exec sleep 30;; esac\nexec " + real + " \"$@\"\n"
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := shortDir(t)
	sock := filepath.Join(dir, "s", "a.sock")
	y := fmt.Sprintf("node:\n  name: hand1\n  listen: [unix://%s]\nstore: sqlite://%s/s/store.db\n"+
		"agents:\n  bot: {driver: inbox}\ncloud: {url: \"http://127.0.0.1:9\"}\n", sock, dir)
	cfg, err := config.Parse([]byte(y), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	cutByTheStop(t, cfg, sock, map[string]func(*client.Client) error{
		"handoff": func(c *client.Client) error {
			_, err := c.Handoff(context.Background(), client.HandoffRequest{Cwd: repo, CloudAgent: "claude"})
			return err
		},
	})
}
