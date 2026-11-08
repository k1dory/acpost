package main

// End to end: the built binary, a node it serves, and the commands an agent or a script would run.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

var binDir string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "acpost-bin")
	if err != nil {
		panic(err)
	}
	binDir = dir
	for pkg, name := range map[string]string{".": "acpost", "../../internal/acptest/fakeagent": "fakeagent"} {
		out, err := exec.Command("go", buildArgs(filepath.Join(dir, name), pkg)...).CombinedOutput()
		if err != nil {
			fmt.Fprintf(os.Stderr, "build %s: %v\n%s", pkg, err, out)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

// env is a sandbox: its own config, state and socket (in /tmp: macOS socket paths are short).
type env struct {
	t      *testing.T
	dir    string
	config string
	sock   string
	vars   []string
	stops  []func() // stop the nodes serve started, latest first
}

// stop stops the node serve started last (SIGTERM, then a wait).
func (e *env) stop() {
	e.t.Helper()
	if n := len(e.stops); n > 0 {
		e.stops[n-1]()
		e.stops = e.stops[:n-1]
	}
}

func newEnv(t *testing.T, yaml string) *env {
	dir, err := os.MkdirTemp("/tmp", "acp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	e := &env{t: t, dir: dir, config: filepath.Join(dir, "config.yaml"), sock: filepath.Join(dir, "s", "acpost.sock")}
	full := fmt.Sprintf("node:\n  name: %s\n  listen: [unix://%s]\nstore: sqlite://%s/s/store.db\n%s",
		"n"+filepath.Base(dir), e.sock, dir, yaml)
	if strings.Contains(yaml, "node:") {
		full = fmt.Sprintf("store: sqlite://%s/s/store.db\n%s", dir, yaml)
	}
	if err := os.WriteFile(e.config, []byte(full), 0o600); err != nil {
		t.Fatal(err)
	}
	e.vars = []string{"ACPOST_CONFIG=" + e.config, "PATH=" + binDir + ":" + os.Getenv("PATH"), "HOME=" + dir}
	return e
}

type result struct {
	out, err string
	code     int
}

func (e *env) run(stdin string, args ...string) result {
	e.t.Helper()
	return e.runFor(childBound, stdin, args...)
}

func cleanEnv() []string {
	var out []string
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "ACPOST_") {
			out = append(out, v)
		}
	}
	return out
}

func (e *env) ok(stdin string, args ...string) string {
	e.t.Helper()
	r := e.run(stdin, args...)
	if r.code != 0 {
		e.t.Fatalf("acpost %v: exit %d\nstdout: %s\nstderr: %s", args, r.code, r.out, r.err)
	}
	return r.out
}

// serve starts a node in the foreground of the test and waits for its socket.
func (e *env) serve(extra ...string) {
	e.t.Helper()
	cmd := exec.Command(filepath.Join(binDir, "acpost"), append([]string{"serve"}, extra...)...)
	cmd.Env = append(cleanEnv(), e.vars...)
	logf, _ := os.Create(filepath.Join(e.dir, "serve.log"))
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cmd.Process.Signal(syscall.SIGTERM)
			done := make(chan struct{})
			go func() { cmd.Wait(); close(done) }()
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				cmd.Process.Kill()
				e.t.Error("node did not stop on SIGTERM")
			}
		})
	}
	e.stops = append(e.stops, stop)
	e.t.Cleanup(func() {
		stop()
		if e.t.Failed() {
			b, _ := os.ReadFile(filepath.Join(e.dir, "serve.log"))
			e.t.Logf("serve log:\n%s", b)
		}
	})
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if r := e.run("", "status"); r.code == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	b, _ := os.ReadFile(filepath.Join(e.dir, "serve.log"))
	e.t.Fatalf("node did not come up:\n%s", b)
}

func fakeAgent(name, extra string) string {
	return fmt.Sprintf("  %s: {driver: acp, command: [%s], preamble: false%s}\n", name, filepath.Join(binDir, "fakeagent"), extra)
}

const inbox = "agents:\n  bot: {driver: inbox}\n"

func TestSendRecvReplyWait(t *testing.T) {
	e := newEnv(t, inbox)
	e.serve()
	id := strings.TrimSpace(e.ok("", "send", "--to", "agent:bot", "--task", "TASK-42", "--body", "Review PR #123"))
	if !strings.HasPrefix(id, "msg_") || strings.Contains(id, " ") {
		t.Fatalf("send printed %q", id)
	}
	var claimed struct {
		ID, Body, Task, Lease, From string
	}
	if err := json.Unmarshal([]byte(e.ok("", "recv", "--as", "bot", "--json")), &claimed); err != nil {
		t.Fatal(err)
	}
	if claimed.ID != id || claimed.Body != "Review PR #123" || claimed.Task != "TASK-42" || claimed.Lease == "" || !strings.HasPrefix(claimed.From, "user:") {
		t.Fatalf("recv %+v", claimed)
	}
	e.ok("LGTM\nwith one nit", "reply", id, "--lease", claimed.Lease)
	if out := e.ok("", "wait", id); out != "LGTM\nwith one nit" {
		t.Fatalf("wait printed %q", out)
	}
}

func TestMessageAlias(t *testing.T) {
	e := newEnv(t, inbox)
	e.serve()
	id := strings.TrimSpace(e.ok("", "message", "send", "--to", "agent:bot", "--task", "TASK-42", "--body", "x"))
	if out := e.ok("", "message", "show", id, "--json"); !strings.Contains(out, `"task":"TASK-42"`) {
		t.Fatal(out)
	}
}

func TestBodyFromStdinAndJSON(t *testing.T) {
	e := newEnv(t, inbox)
	e.serve()
	var m map[string]any
	if err := json.Unmarshal([]byte(e.ok("multi\nline body\n", "send", "--to", "bot", "--json")), &m); err != nil {
		t.Fatal(err)
	}
	if m["body"] != "multi\nline body\n" || m["state"] != "queued" || m["to"] != "agent:bot" {
		t.Fatalf("%v", m)
	}
}

func TestExitCodes(t *testing.T) {
	e := newEnv(t, inbox+"limits: {max_body: 10}\n")
	e.serve()
	id := strings.TrimSpace(e.ok("", "send", "--to", "bot", "--body", "x"))
	for name, tc := range map[string]struct {
		args []string
		code int
	}{
		"unknown message": {[]string{"wait", "msg_00000000000000000000000000"}, 3},
		"unknown agent":   {[]string{"send", "--to", "ghost", "--body", "x"}, 3},
		"wait timeout":    {[]string{"wait", id, "--timeout", "200ms"}, 4},
		"too big":         {[]string{"send", "--to", "bot", "--body", "01234567890"}, 7},
		"no node":         {[]string{"--node", "unix://" + e.dir + "/none.sock", "status"}, 6},
		"bad usage":       {[]string{"send", "--body", "x"}, 2},
		"bad flag":        {[]string{"send", "--nope"}, 2},
	} {
		t.Run(name, func(t *testing.T) {
			if r := e.run("", tc.args...); r.code != tc.code {
				t.Fatalf("exit %d, want %d\n%s%s", r.code, tc.code, r.out, r.err)
			}
		})
	}
	e.ok("", "cancel", id)
	if r := e.run("", "wait", id); r.code != 5 || !strings.Contains(r.err, "cancelled") {
		t.Fatalf("wait cancelled: %d %s", r.code, r.err)
	}
	if r := e.run("", "wait", "msg_nope", "--json"); r.code != 3 || !strings.Contains(r.err, `"code":"not_found"`) {
		t.Fatalf("json error: %d %q", r.code, r.err)
	}
}

func TestIdempotencyKey(t *testing.T) {
	e := newEnv(t, inbox)
	e.serve()
	a := e.ok("", "send", "--to", "bot", "--body", "x", "--idempotency-key", "k1")
	b := e.ok("", "send", "--to", "bot", "--body", "x", "--idempotency-key", "k1")
	if a != b {
		t.Fatalf("%s != %s", a, b)
	}
}

func TestListNDJSON(t *testing.T) {
	e := newEnv(t, inbox)
	e.serve()
	for i := range 7 {
		e.ok("", "send", "--to", "bot", "--body", fmt.Sprint(i), "--task", "T")
	}
	var ids []string
	cursor := ""
	for {
		args := []string{"list", "--task", "T", "--limit", "3", "--json"}
		if cursor != "" {
			args = append(args, "--after", cursor)
		}
		cursor = ""
		for _, line := range strings.Split(strings.TrimSpace(e.ok("", args...)), "\n") {
			var obj map[string]any
			if err := json.Unmarshal([]byte(line), &obj); err != nil {
				t.Fatalf("not NDJSON: %q", line)
			}
			if n, ok := obj["next"]; ok {
				cursor = n.(string)
				continue
			}
			if _, has := obj["body"]; has {
				t.Fatal("list carries bodies")
			}
			ids = append(ids, obj["id"].(string))
		}
		if cursor == "" {
			break
		}
	}
	if len(ids) != 7 {
		t.Fatalf("%d ids", len(ids))
	}
}

func TestACPSendWait(t *testing.T) {
	e := newEnv(t, "agents:\n"+fakeAgent("reviewer", ""))
	e.serve()
	if out := e.ok("", "send", "--to", "agent:reviewer", "--body", "echo looks good", "--wait", "--timeout", "30s"); out != "looks good" {
		t.Fatalf("%q", out)
	}
}

// Claude asks Codex, Codex asks a third agent: an agent sends from its own shell with the
// environment its node gave it, and the chain is recorded.
func TestAgentToAgentChain(t *testing.T) {
	e := newEnv(t, "agents:\n"+fakeAgent("a", "")+fakeAgent("b", ""))
	e.serve()
	top := strings.TrimSpace(e.ok("", "send", "--to", "a", "--task", "T-7", "--body", "shell acpost send --to b --body 'echo from deep' --wait --timeout 20s"))
	if out := e.ok("", "wait", top, "--timeout", "30s"); strings.TrimSpace(out) != "from deep" {
		t.Fatalf("chain reply %q", out)
	}
	var page []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(e.ok("", "list", "--to", "agent:b", "--json")), "\n") {
		var obj map[string]any
		json.Unmarshal([]byte(line), &obj)
		if obj["id"] != nil {
			page = append(page, obj)
		}
	}
	if len(page) != 1 {
		t.Fatalf("%d messages to b", len(page))
	}
	child := page[0]
	if child["from"] != "agent:a" || child["parent"] != top || child["depth"] != float64(1) || child["task"] != "T-7" {
		t.Fatalf("child %v", child)
	}
}

func TestAutostart(t *testing.T) {
	e := newEnv(t, inbox)
	r := e.run("", "send", "--to", "bot", "--body", "x")
	if r.code != 0 {
		t.Fatalf("send without a node: %d %s", r.code, r.err)
	}
	pidb, err := os.ReadFile(filepath.Join(filepath.Dir(e.sock), "acpost.pid"))
	if err != nil {
		t.Fatal(err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(pidb)))
	t.Cleanup(func() { syscall.Kill(pid, syscall.SIGTERM) })
	if out := e.ok("", "status", "--json"); !strings.Contains(out, `"ok":true`) {
		t.Fatal(out)
	}
	// A second command reuses the node instead of starting another.
	e.ok("", "send", "--to", "bot", "--body", "y")
	pidb2, _ := os.ReadFile(filepath.Join(filepath.Dir(e.sock), "acpost.pid"))
	if string(pidb2) != string(pidb) {
		t.Fatal("a second node was started")
	}
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// sha256("secret")
const secretSHA = "2bb80d537b1da3e38bd30361aa855686bde0eacd7162fef6a25fe97bf527a25b"

func TestTCPWithToken(t *testing.T) {
	port := freePort(t)
	sock := ""
	e := newEnv(t, fmt.Sprintf(`node:
  name: tcp
  listen: [tcp://127.0.0.1:%d]
tokens:
  - {name: ci, sha256: %s, send_to: ["agent:bot"], act_as: [bot]}
%s`, port, secretSHA, inbox))
	_ = sock
	url := fmt.Sprintf("http://127.0.0.1:%d", port)
	cmd := exec.Command(filepath.Join(binDir, "acpost"), "serve")
	cmd.Env = append(cleanEnv(), e.vars...)
	cmd.Start()
	t.Cleanup(func() { cmd.Process.Signal(syscall.SIGTERM); cmd.Wait() })
	deadline := time.Now().Add(10 * time.Second)
	for e.run("", "--node", url, "--token", "secret", "status").code != 0 {
		if time.Now().After(deadline) {
			t.Fatal("tcp node did not come up")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if r := e.run("", "--node", url, "send", "--to", "bot", "--body", "x"); r.code != 7 {
		t.Fatalf("no token: exit %d", r.code)
	}
	if r := e.run("", "--node", url, "--token", "wrong", "send", "--to", "bot", "--body", "x"); r.code != 7 {
		t.Fatalf("wrong token: exit %d", r.code)
	}
	id := strings.TrimSpace(e.ok("", "--node", url, "--token", "secret", "send", "--to", "bot", "--body", "x"))
	var m map[string]any
	json.Unmarshal([]byte(e.ok("", "--node", url, "--token", "secret", "show", id, "--json")), &m)
	if m["from"] != "client:ci" {
		t.Fatalf("from %v", m["from"])
	}
}

// Many clients at once through the HTTP API: every send succeeds and is stored once.
func TestConcurrentHTTPClients(t *testing.T) {
	e := newEnv(t, "limits: {max_queued_per_agent: 100000}\n"+inbox)
	e.serve()
	client := &http.Client{Transport: &http.Transport{DialContext: func(_ ctxT, _, _ string) (net.Conn, error) {
		return net.Dial("unix", e.sock)
	}}}
	const clients, each = 32, 40
	var wg sync.WaitGroup
	var mu sync.Mutex
	var failures []string
	for c := range clients {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				body := fmt.Sprintf(`{"to":"bot","body":"c%d-%d"}`, c, i)
				resp, err := client.Post("http://acpost/v1/messages", "application/json", strings.NewReader(body))
				if err != nil {
					mu.Lock()
					failures = append(failures, err.Error())
					mu.Unlock()
					continue
				}
				b, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 201 {
					mu.Lock()
					failures = append(failures, fmt.Sprintf("%d %s", resp.StatusCode, b))
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()
	if len(failures) > 0 {
		t.Fatalf("%d failures, first: %s", len(failures), failures[0])
	}
	var agents []map[string]any
	json.Unmarshal([]byte(e.ok("", "agents", "--json")), &agents)
	if agents[0]["queued"] != float64(clients*each) {
		t.Fatalf("queued %v, want %d", agents[0]["queued"], clients*each)
	}
}

// hubPair starts a hub and two nodes joined to it: a serves agent:reviewer (a fake agent), b none.
// extra lines go into the configuration of b, of the hub and of a.
// lastHub is the hub of the last hubPair (tests here do not run in parallel).
var lastHub, lastHubA *env

// hubEnv is the hub b joined in hubPair.
func hubEnv(t *testing.T, _ *env) *env {
	if lastHub == nil {
		t.Fatal("no hubPair")
	}
	return lastHub
}

// extra: b's configuration, the hub's, a's (before agents:), and more of a's reviewer (", env: …").
func hubPair(t *testing.T, extra ...string) (a, b *env) {
	extra = append(extra, "", "", "", "")
	port := freePort(t)
	hubURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	a = newEnv(t, fmt.Sprintf("%sagents:\n%s", extra[2], fakeAgent("reviewer", ", allow_from: [\"*\"]"+extra[3])))
	a.vars = append(a.vars, "ACPOST_JOIN_TOKEN=node-a-token")
	appendCfg(t, a, fmt.Sprintf("join_hub: {url: %s, token_env: ACPOST_JOIN_TOKEN}\n", hubURL))
	b = newEnv(t, extra[0])
	b.vars = append(b.vars, "ACPOST_JOIN_TOKEN=node-b-token")
	appendCfg(t, b, fmt.Sprintf("join_hub: {url: %s, token_env: ACPOST_JOIN_TOKEN}\n", hubURL))

	// A node's token on the hub carries the node's name.
	hub := newEnv(t, fmt.Sprintf(`node:
  name: hub
  listen: [tcp://127.0.0.1:%d]
tokens:
  - {name: %s, sha256: %s, node: true}
  - {name: %s, sha256: %s, node: true}
%s`, port, nodeName(a), sha("node-a-token"), nodeName(b), sha("node-b-token"), extra[1]))
	// The hub's own CLI talks to it on a socket of its own.
	cfg, _ := os.ReadFile(hub.config)
	os.WriteFile(hub.config, []byte(strings.Replace(string(cfg), fmt.Sprintf("listen: [tcp://127.0.0.1:%d]", port),
		fmt.Sprintf("listen: [tcp://127.0.0.1:%d, unix://%s]", port, hub.sock), 1)), 0o600)
	lastHub, lastHubA = hub, a
	hubCmd := exec.Command(filepath.Join(binDir, "acpost"), "serve")
	hubCmd.Env = append(cleanEnv(), hub.vars...)
	hubLog, _ := os.Create(filepath.Join(hub.dir, "serve.log"))
	hubCmd.Stdout, hubCmd.Stderr = hubLog, hubLog
	hubCmd.Start()
	t.Cleanup(func() {
		hubCmd.Process.Signal(syscall.SIGTERM)
		hubCmd.Wait()
		if t.Failed() {
			b, _ := os.ReadFile(filepath.Join(hub.dir, "serve.log"))
			t.Logf("hub log:\n%s", b)
		}
	})

	a.serve()
	b.serve()
	return a, b
}

// Two nodes join a hub; a message sent on node B to an agent of node A is answered by A's agent.
func TestHubJoin(t *testing.T) {
	a, b := hubPair(t)

	deadline := time.Now().Add(15 * time.Second)
	for {
		r := b.run("", "send", "--to", "agent:reviewer", "--body", "echo across nodes", "--wait", "--timeout", "20s")
		if r.code == 0 {
			if r.out != "across nodes" {
				t.Fatalf("%q", r.out)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never routed: %d %s", r.code, r.err)
		}
		time.Sleep(200 * time.Millisecond) // the announce may not have reached the hub yet
	}
	// The same through an explicit node name.
	if out := b.ok("", "send", "--to", "agent:reviewer@"+nodeName(a), "--body", "echo named", "--wait", "--timeout", "20s"); out != "named" {
		t.Fatalf("%q", out)
	}
	// B lists the agents on the other side of the hub, and not its own twice.
	out := b.ok("", "agents")
	if !strings.Contains(out, "agent:reviewer@"+nodeName(a)) || !strings.Contains(out, "remote") {
		t.Fatalf("agents on b: %s", out)
	}
	if out := a.ok("", "agents"); strings.Count(out, "reviewer") != 1 {
		t.Fatalf("agents on a lists its own twice: %s", out)
	}
}

func nodeName(e *env) string { return "n" + filepath.Base(e.dir) }

// appendCfg adds join settings under node: (the generated config has node: first).
func appendCfg(t *testing.T, e *env, line string) {
	b, _ := os.ReadFile(e.config)
	s := strings.Replace(string(b), "node:\n", "node:\n  "+strings.Replace(line, "join_hub:", "join:", 1), 1)
	os.WriteFile(e.config, []byte(s), 0o600)
}

// An agent reading stderr learns what failed and what to do; with --json it gets fields.
func TestErrorsExplainThemselves(t *testing.T) {
	state, err := os.MkdirTemp("/tmp", "acpst")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(state)
	os.WriteFile(state+"/s1", []byte("1"), 0o600)
	e := newEnv(t, fmt.Sprintf("agents:\n  fake: {driver: acp, command: [%s], preamble: false, env: {FAKE_STATE: %s}}\n", filepath.Join(binDir, "fakeagent"), state))
	e.serve()

	if out := e.ok("", "sessions", "--agent", "fake"); !strings.Contains(out, "s1") {
		t.Fatalf("sessions: %q", out)
	}
	r := e.run("", "send", "--to", "fake", "--session", "busy-1", "--body", "count", "--wait", "--timeout", "20s")
	if r.code != 5 || !strings.Contains(r.err, "[session_busy]") || !strings.Contains(r.err, "hint:") || strings.Contains(r.err, "-32600") {
		t.Fatalf("plain: exit %d\n%s", r.code, r.err)
	}
	r = e.run("", "send", "--to", "fake", "--session", "busy-1", "--body", "count", "--wait", "--timeout", "20s", "--json")
	var m map[string]any
	if err := json.Unmarshal([]byte(r.out), &m); err != nil || m["error_code"] != "session_busy" || m["hint"] == nil {
		t.Fatalf("json message: %v %q", err, r.out)
	}
	r = e.run("", "send", "--to", "ghost", "--body", "x", "--json")
	var eb struct {
		Error struct{ Code, Message, Hint string }
	}
	if err := json.Unmarshal([]byte(r.err), &eb); err != nil || eb.Error.Code != "not_found" || !strings.Contains(eb.Error.Hint, "acpost agents") {
		t.Fatalf("json error: %v %q", err, r.err)
	}
}

// Found by Codex testing acpost as an agent (DUO-1).
func TestAgentFeedbackDUO1(t *testing.T) {
	e := newEnv(t, inbox)
	e.serve()
	// A timed-out send --wait names the message and how to continue.
	r := e.run("", "send", "--to", "bot", "--body", "x", "--wait", "--timeout", "200ms")
	if r.code != 4 || !strings.Contains(r.err, "msg_") || !strings.Contains(r.err, "acpost wait msg_") {
		t.Fatalf("timeout: exit %d\n%s", r.code, r.err)
	}
	// --json holds whatever the order of the flags, and from the environment.
	for _, args := range [][]string{{"status", "--bogus", "--json"}, {"status", "--bogus"}} {
		cmd := exec.Command(filepath.Join(binDir, "acpost"), args...)
		cmd.Env = append(cleanEnv(), append(e.vars, "ACPOST_JSON=1")...)
		out, _ := cmd.CombinedOutput()
		var eb struct{ Error struct{ Code string } }
		if json.Unmarshal(out, &eb) != nil || eb.Error.Code != "invalid" {
			t.Fatalf("%v: not JSON: %s", args, out)
		}
	}
	// status says what an agent needs to know in one call: the node, who you are, the agents.
	if out := e.ok("", "status"); !strings.Contains(out, "agent:bot") {
		t.Fatalf("status lacks agents:\n%s", out)
	}
	var st map[string]any
	if err := json.Unmarshal([]byte(e.ok("", "status", "--json")), &st); err != nil || st["agents"] == nil {
		t.Fatalf("status --json: %v %v", err, st)
	}
}

func TestSocketPermissionHint(t *testing.T) {
	err := fmt.Errorf("%w: unix:///x: %w", client.ErrUnavailable, syscall.EPERM)
	if h := hintOf(err); !strings.Contains(h, "sandbox") {
		t.Fatalf("EPERM hint: %q", h)
	}
	if h := hintOf(fmt.Errorf("%w: unix:///x: %w", client.ErrUnavailable, syscall.ENOENT)); !strings.Contains(h, "acpost serve") {
		t.Fatalf("ENOENT hint: %q", h)
	}
}

// show --follow prints the agent's text as it writes it and ends with the outcome; --json gives
// the events and the final message; an unknown id is a plain error. Through a hub the answer
// comes whole at the end.
func TestShowFollow(t *testing.T) {
	e := newEnv(t, "agents:\n"+fakeAgent("fake", ""))
	e.serve()
	id := strings.TrimSpace(e.ok("", "send", "--to", "fake", "--body", "stream 6 150"))
	r := e.run("", "show", id, "--follow")
	if r.code != 0 || r.out != "w1 w2 w3 w4 w5 w6\n" {
		t.Fatalf("%d %q %q", r.code, r.out, r.err)
	}
	r = e.run("", "show", id, "--follow", "--json")
	lines := strings.Split(strings.TrimSpace(r.out), "\n")
	if r.code != 0 || len(lines) < 3 || !strings.Contains(lines[0], `"kind":"thought"`) || !strings.Contains(lines[len(lines)-1], `"state":"done"`) {
		t.Fatalf("%d %s", r.code, r.out)
	}
	// With reply: last the text before a tool call is streamed but is not the reply: the reply follows.
	id = strings.TrimSpace(e.ok("", "send", "--to", "fake", "--body", "tools"))
	if r := e.run("", "show", id, "--follow"); r.code != 0 || !strings.HasSuffix(r.out, "\n--- reply\nfinal\n") {
		t.Fatalf("tools: %d %q %q", r.code, r.out, r.err)
	}
	if r := e.run("", "show", "msg_01M6RT5P63ENPZ7GH7083B56P2", "--follow"); r.code != 3 || !strings.Contains(r.err, "no message") {
		t.Fatalf("unknown id: %d %s", r.code, r.err)
	}

	a, b := hubPair(t)
	_ = a
	deadline := time.Now().Add(15 * time.Second)
	for {
		r := b.run("", "send", "--to", "agent:reviewer", "--body", "echo across")
		if r.code == 0 {
			if f := b.run("", "show", strings.TrimSpace(r.out), "--follow"); f.code != 0 || f.out != "across\n" {
				t.Fatalf("remote follow: %d %q %q", f.code, f.out, f.err)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never routed: %d %s", r.code, r.err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// An answer written on another node streams to the sender's node through the hub: the text comes
// while the turn runs, in order, and adds up to the reply.
func TestFollowAcrossHub(t *testing.T) {
	_, b := hubPair(t)
	var id string
	deadline := time.Now().Add(15 * time.Second)
	for {
		r := b.run("", "send", "--to", "agent:reviewer", "--body", "stream 10 200")
		if r.code == 0 {
			id = strings.TrimSpace(r.out)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never routed: %d %s", r.code, r.err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	c, err := client.New("unix://"+b.sock, "", "")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var first time.Duration
	var runningAtFirst bool
	var text strings.Builder
	var kinds []string
	m, err := c.Follow(context.Background(), id, 0, func(e store.Event) error {
		kinds = append(kinds, e.Kind)
		if e.Kind == "text" {
			if first == 0 {
				first = time.Since(start)
				// Asked now, not timed: the message is still going when its text starts to come.
				if now, err := c.Get(context.Background(), id); err == nil && !now.State.Final() {
					runningAtFirst = true
				}
			}
			var d map[string]string
			json.Unmarshal([]byte(e.Data), &d)
			text.WriteString(d["text"])
		}
		return nil
	})
	total := time.Since(start)
	if err != nil || m.State != store.Done || text.String() != m.Reply || !strings.HasPrefix(m.Reply, "w1 w2") {
		t.Fatalf("%v %v: streamed %q, reply %q (%v)", m.State, err, text.String(), m.Reply, kinds)
	}
	if len(kinds) < 3 || kinds[0] != "thought" {
		t.Fatalf("kinds %v", kinds)
	}
	if !runningAtFirst {
		t.Fatalf("the first text came at %v of %v, once the message was final: not while the turn ran", first, total)
	}
	// What came is kept on b's copy: show --follow later prints it whole.
	if r := b.run("", "show", id, "--follow"); r.code != 0 || r.out != m.Reply+"\n" {
		t.Fatalf("again: %d %q %q", r.code, r.out, r.err)
	}
}

// The sender's node restarted while the agent wrote: the events it missed come from the hub's
// pages before its copy is final.
func TestFollowAcrossHubAfterRestart(t *testing.T) {
	a, b := hubPair(t)
	var id string
	deadline := time.Now().Add(15 * time.Second)
	for {
		r := b.run("", "send", "--to", "agent:reviewer", "--body", "stream 10 300")
		if r.code == 0 {
			id = strings.TrimSpace(r.out)
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("never routed: %d %s", r.code, r.err)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// Stopped once its text has started to come, started again once the agent wrote more.
	if !until(15*time.Second, func() bool { return b.textEvents(id) > 0 }) {
		t.Fatal("no text reached the sender's node")
	}
	seen := b.textEvents(id)
	b.stop()
	if !until(15*time.Second, func() bool { return a.textEvents(id) > seen }) {
		t.Fatalf("the agent's node has %d text events, the sender's had %d", a.textEvents(id), seen)
	}
	b.serve()
	out := b.ok("", "wait", id, "--timeout", "30s")
	evs := b.ok("", "show", id, "--follow", "--json")
	var text strings.Builder
	for _, l := range strings.Split(strings.TrimSpace(evs), "\n") {
		var e store.Event
		if json.Unmarshal([]byte(l), &e) == nil && e.Kind == "text" {
			var d map[string]string
			json.Unmarshal([]byte(e.Data), &d)
			text.WriteString(d["text"])
		}
	}
	if text.String() != out {
		t.Fatalf("events after a restart: %q, reply %q", text.String(), out)
	}
}

// What an agent on another node used reaches the sender's node with the turn's events: tokens,
// the cost by the agent's account turn by turn, an estimate by the sender's prices for a turn
// without one, the cache's share.
func TestUsageAcrossHub(t *testing.T) {
	_, b := hubPair(t, "prices: {Fake-big: {input: 1, output: 2, cache_read: 0.1}}\n", "", "", ", model: fake-big")
	send := func(body string) {
		deadline := time.Now().Add(15 * time.Second)
		for {
			r := b.run("", "send", "--to", "agent:reviewer", "--task", "T", "--body", body, "--wait", "--timeout", "20s")
			if r.code == 0 {
				return
			}
			if time.Now().After(deadline) || (r.code != 3 && r.code != 6) {
				t.Fatalf("exit %d %s %s", r.code, r.out, r.err)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	send("usage 100 10 900 0 0.1")
	send("usage 200 20 800 0 0.3")
	send("usage 1000000 0 0 0") // no cost: 1 M input at 1 per M
	out := b.ok("", "usage", "--json")
	var r client.UsageReport
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &r); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if r.Key != "agent:reviewer@"+nodeName(lastHubA) || r.Turns != 3 || r.Input != 1000300 || r.CacheRead != 1700 {
		t.Fatalf("%+v", r)
	}
	if r.Costed != 2 || r.Cost < 0.299 || r.Cost > 0.301 || r.EstimatedTurns != 1 || r.Estimated != 1 || r.EstimatedCurrency != "USD" {
		t.Fatalf("cost: %+v", r)
	}
	if want := 1700.0 / (1000300 + 1700); r.CacheRatio < want-1e-9 || r.CacheRatio > want+1e-9 {
		t.Fatalf("cache ratio %v, want %v", r.CacheRatio, want)
	}
	table := b.ok("", "usage")
	if !strings.Contains(table, "0.30 USD (~1.00 est.)") && !strings.Contains(table, "1.30 USD (~1.00 est.)") {
		t.Fatalf("table: %s", table)
	}
}

// What an agent changed in its repository, with what the agent it asked changed: per turn, and the
// repository's whole diff; nothing outside a repository.
func TestChangesWithSubagent(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "init"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("%v %s", err, out)
		}
	}
	e := newEnv(t, "agents:\n"+fakeAgent("lead", ", cwd: "+repo)+fakeAgent("helper", ", cwd: "+repo))
	e.serve()
	id := strings.TrimSpace(e.ok("", "send", "--to", "lead", "--body",
		"shell echo lead > lead.txt && acpost send --to helper --body 'shell echo help > help.txt' --wait --timeout 20s"))
	e.ok("", "wait", id, "--timeout", "30s")

	var ch client.Changes
	json.Unmarshal([]byte(e.ok("", "changes", id, "--tree", "--json")), &ch)
	agents := map[string]bool{}
	for _, t := range ch.Turns {
		agents[strings.SplitN(t.Agent, "@", 2)[0]] = true
	}
	if len(ch.Turns) != 2 || !agents["agent:lead"] || !agents["agent:helper"] {
		t.Fatalf("%+v", ch.Turns)
	}
	if len(ch.Repos) != 1 || !strings.Contains(ch.Repos[0].Diff, "+lead") || !strings.Contains(ch.Repos[0].Diff, "+help") {
		t.Fatalf("%+v", ch.Repos)
	}
	if stat := e.ok("", "changes", id, "--tree", "--stat"); !strings.Contains(stat, "A help.txt") || !strings.Contains(stat, "+1 -0") {
		t.Fatalf("%s", stat)
	}
	// Without --tree: the lead's own turn (which saw the helper's file appear too).
	var own client.Changes
	json.Unmarshal([]byte(e.ok("", "changes", id, "--json")), &own)
	if len(own.Turns) != 1 {
		t.Fatalf("%+v", own.Turns)
	}
	evs := e.ok("", "show", id, "--events")
	if !strings.Contains(evs, " changes ") || !strings.Contains(evs, "lead.txt") {
		t.Fatalf("no changes event: %s", evs)
	}
}

// A terminal through the API, in an agent's working directory.
func TestTerminalOverAPI(t *testing.T) {
	dir := t.TempDir()
	e := newEnv(t, "agents:\n"+fakeAgent("worker", ", cwd: "+dir))
	e.serve()
	c, err := client.New("unix://"+e.sock, "", "")
	if err != nil {
		t.Fatal(err)
	}
	tm, err := c.OpenTerminal(context.Background(), client.TermRequest{Agent: "worker"})
	if err != nil || tm.Cwd != dir {
		t.Fatalf("%+v %v", tm, err)
	}
	if err := c.TermInput(context.Background(), tm.ID, []byte("pwd; echo via-\"\"http; exit\n")); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var out strings.Builder
	var next int64
	end, err := c.TermOutput(ctx, tm.ID, 0, func(n int64, b []byte) error { out.Write(b); next = n; return nil })
	if next != end.Size {
		t.Fatalf("offset %d, size %d", next, end.Size)
	}
	if err != nil || end.Running || !strings.Contains(out.String(), "via-http") {
		t.Fatalf("%+v %v %q", end, err, out.String())
	}
	if _, err := c.OpenTerminal(context.Background(), client.TermRequest{Agent: "nobody"}); err == nil {
		t.Fatal("a terminal of no agent")
	}
}
