//go:build darwin

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// The fake VS Code CLI is this test binary, run by a shell script in VS Code's place:
// ACPOST_FAKE_CLI is its behaviour. It answers serve-web as code-tunnel does (prints "Web UI
// available at http://127.0.0.1:PORT?tkn=…" and serves the workbench page), or misbehaves.
func TestIDEFakeCLI(t *testing.T) {
	mode := os.Getenv("ACPOST_FAKE_CLI")
	if mode == "" {
		t.Skip("the fake VS Code CLI (run by the lifecycle tests)")
	}
	args := os.Args
	for i, a := range args {
		if a == "--" {
			args = args[i+1:]
			break
		}
	}
	fakeCLI(mode, args)
	os.Exit(0)
}

func fakeCLI(mode string, args []string) {
	var port, tokFile string
	for i := 0; i+1 < len(args); i++ {
		switch args[i] {
		case "--port":
			port = args[i+1]
		case "--connection-token-file":
			tokFile = args[i+1]
		}
	}
	tok, _ := os.ReadFile(tokFile)
	if log := os.Getenv("ACPOST_FAKE_LOG"); log != "" {
		f, _ := os.OpenFile(log, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
		fmt.Fprintln(f, os.Getpid())
		f.Close()
	}
	switch mode {
	case "lingering":
		// The CLI: its server is a child of its, which ends in order (a moment after SIGTERM),
		// while the CLI itself ends at once.
		c := exec.Command(os.Args[0], os.Args[1:]...)
		c.Env = append(os.Environ(), "ACPOST_FAKE_CLI=child", "ACPOST_FAKE_LOG=")
		// Its output passed on by the CLI, as code-tunnel does code-server's (the child does not
		// hold the CLI's own output).
		out, _ := c.StdoutPipe()
		if err := c.Start(); err != nil {
			fmt.Println("child:", err)
			os.Exit(1)
		}
		io.Copy(os.Stdout, out)
		c.Wait()
		os.Exit(0)
	case "child":
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGTERM)
		go func() {
			<-sig
			time.Sleep(300 * time.Millisecond)
			os.WriteFile(os.Getenv("ACPOST_FAKE_PIDS")+".graceful", []byte("ok"), 0o600)
			os.Exit(0)
		}()
	case "boom":
		fmt.Printf("error getting the server: boom (from http://127.0.0.1:1?tkn=%s)\n", tok)
		os.Exit(1)
	case "silent":
		time.Sleep(time.Hour)
	case "stubborn":
		signal.Ignore(syscall.SIGTERM)
	}
	l, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		fmt.Println("listen:", err)
		os.Exit(1)
	}
	if mode == "longline" {
		fmt.Println(strings.Repeat("x", 100<<10))
	}
	if mode == "grandchild" {
		// A child holding the output pipe, as VS Code's own server can.
		c := exec.Command("sleep", "1000")
		c.Stdout = os.Stdout
		c.Start()
		os.WriteFile(os.Getenv("ACPOST_FAKE_PIDS"), []byte(strconv.Itoa(c.Process.Pid)), 0o600)
	}
	page := `<meta id="vscode-workbench-web-configuration" data-settings="{}">`
	if mode == "downloading" {
		page = "The latest version of the Visual Studio Code Server is downloading, please wait a moment..."
	}
	fmt.Printf("Web UI available at http://127.0.0.1:%d?tkn=%s\n", l.Addr().(*net.TCPAddr).Port, tok)
	mux := http.NewServeMux()
	mux.HandleFunc("/exit", func(w http.ResponseWriter, r *http.Request) {
		go func() { time.Sleep(50 * time.Millisecond); os.Exit(0) }()
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, page) })
	http.Serve(l, mux)
}

// fakeIDE is an IDE server whose VS Code is the fake CLI in mode, on a port of its own.
type fakeIDE struct {
	*ideServer
	data, log, pids string
}

func newFakeIDE(t *testing.T, mode string) *fakeIDE {
	t.Helper()
	dir := t.TempDir()
	f := &fakeIDE{data: filepath.Join(dir, "vscode"), log: filepath.Join(dir, "starts"), pids: filepath.Join(dir, "pids")}
	os.MkdirAll(f.data, 0o700)
	cli := filepath.Join(dir, "code-tunnel")
	script := fmt.Sprintf("#!/bin/sh\nACPOST_FAKE_CLI=%s ACPOST_FAKE_LOG=%s ACPOST_FAKE_PIDS=%s exec %s -test.run='^TestIDEFakeCLI$' -- \"$@\"\n",
		mode, f.log, f.pids, strconv.Quote(os.Args[0]))
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	f.ideServer = &ideServer{
		findCLI: func() (string, error) { return cli, nil },
		dataDir: func() (string, error) { return f.data, nil },
		install: func(ctx context.Context, data string) error { return nil },
		port:    freePort(t),
		budget:  20 * time.Second,
	}
	t.Cleanup(f.stop)
	return f
}

func freePort(t *testing.T) int {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

// starts is how many times the fake CLI was run.
func (f *fakeIDE) starts() int {
	b, _ := os.ReadFile(f.log)
	return strings.Count(string(b), "\n")
}

func (f *fakeIDE) current() *ideRun {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.run
}

func portOf(t *testing.T, base string) int {
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	p, _ := strconv.Atoi(u.Port())
	return p
}

func waitClosed(c chan struct{}, d time.Duration) bool {
	select {
	case <-c:
		return true
	case <-time.After(d):
		return false
	}
}

func tokenFiles(t *testing.T) map[string]bool {
	m, _ := filepath.Glob(filepath.Join(os.TempDir(), "acpost-vscode-token-*"))
	out := map[string]bool{}
	for _, f := range m {
		out[f] = true
	}
	return out
}

// One start for every caller at once; the address serves, with the folder and the token.
func TestIDEStartOnceForAll(t *testing.T) {
	f := newFakeIDE(t, "serve")
	var wg sync.WaitGroup
	bases := make([]string, 8)
	errs := make([]error, 8)
	for i := range bases {
		wg.Add(1)
		go func(i int) { defer wg.Done(); bases[i], errs[i] = f.open("/tmp/x") }(i)
	}
	wg.Wait()
	for i := range bases {
		if errs[i] != nil || bases[i] != bases[0] {
			t.Fatalf("start %d: %q %v (first %q)", i, bases[i], errs[i], bases[0])
		}
	}
	if n := f.starts(); n != 1 {
		t.Fatalf("the CLI ran %d times", n)
	}
	u, _ := url.Parse(bases[0])
	if u.Query().Get("folder") != "/tmp/x" || len(u.Query().Get("tkn")) != 48 || portOf(t, bases[0]) != f.port {
		t.Fatalf("address %s (port %d)", bases[0], f.port)
	}
	// Served now: a second open is the same server.
	if b, err := f.open("/tmp/x"); err != nil || b != bases[0] || f.starts() != 1 {
		t.Fatalf("again: %q %v, %d starts", b, err, f.starts())
	}
}

// I2/Z3: a CLI that ends while a child of its holds the output pipe is seen to end (its child
// with it), its token file is removed, and the next IDEStart starts it again.
func TestIDEEndSeenWithGrandchild(t *testing.T) {
	f := newFakeIDE(t, "grandchild")
	before := tokenFiles(t)
	base, err := f.open("")
	if err != nil {
		t.Fatal(err)
	}
	run := f.current()
	var mine []string
	for tf := range tokenFiles(t) {
		if !before[tf] {
			mine = append(mine, tf)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("token files of this server: %v", mine)
	}
	u, _ := url.Parse(base)
	http.Get("http://" + u.Host + "/exit")
	if !waitClosed(run.done, 4*time.Second) {
		t.Fatal("the server's end is not seen while its child holds the pipe")
	}
	if _, err := os.Stat(mine[0]); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("token file left: %v", err)
	}
	b, _ := os.ReadFile(f.pids)
	// Gone when done is closed (no further wait): done is the group's end.
	if pid, _ := strconv.Atoi(string(b)); pid == 0 || !gone(pid, 50*time.Millisecond) {
		t.Errorf("the CLI's child (%q) still runs when its end is seen", b)
	}
	if _, err := os.Stat(filepath.Join(f.data, idePidFile)); err == nil {
		t.Error("pid file left")
	}
	if _, err := f.open(""); err != nil || f.starts() != 2 {
		t.Fatalf("not started again: %v, %d starts", err, f.starts())
	}
}

// Z4: a line longer than any buffer does not stop the reading (the address after it is seen).
func TestIDELongLine(t *testing.T) {
	f := newFakeIDE(t, "longline")
	f.budget = 5 * time.Second
	if _, err := f.open(""); err != nil {
		t.Fatal(err)
	}
}

// I6: a start that fails says what VS Code said, without the token.
func TestIDEStartErrorSaysWhy(t *testing.T) {
	f := newFakeIDE(t, "boom")
	_, err := f.open("")
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("error: %v", err)
	}
	if ideTkn.FindString(err.Error()) != "tkn=…" {
		t.Fatalf("token not taken out: %v", err)
	}
}

// Z3: stop ends a server that ignores SIGTERM (SIGKILL after the wait) and waits for its end;
// nothing starts after it.
func TestIDEStopEscalates(t *testing.T) {
	old := ideTermWait
	ideTermWait = 300 * time.Millisecond
	defer func() { ideTermWait = old }()
	f := newFakeIDE(t, "stubborn")
	if _, err := f.open(""); err != nil {
		t.Fatal(err)
	}
	run := f.current()
	start := time.Now()
	f.stop()
	if !closed(run.done) {
		t.Fatal("stop returned before the server ended")
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Errorf("stop took %v", d)
	}
	if !portFree(f.port) {
		t.Error("the port is still taken")
	}
	if _, err := f.open(""); !errors.Is(err, errIDEQuitting) {
		t.Fatalf("a start after stop: %v", err)
	}
}

// Z3: a stop while a start is under way ends that start and its process.
func TestIDEStopDuringStart(t *testing.T) {
	f := newFakeIDE(t, "silent")
	res := make(chan error, 1)
	go func() { _, err := f.open(""); res <- err }()
	for end := time.Now().Add(5 * time.Second); f.current() == nil && time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
	}
	run := f.current()
	if run == nil {
		t.Fatal("not started")
	}
	f.stop()
	select {
	case err := <-res:
		if !errors.Is(err, errIDEQuitting) {
			t.Fatalf("start under a stop: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the start did not end with stop")
	}
	if !waitClosed(run.done, 3*time.Second) {
		t.Fatal("its process still runs")
	}
}

// I1/Z8: a server left by an app that did not end it (crashed: its lock gone with it) is ended
// by the next app's start, which takes the same port (VS Code's settings and state are the port's).
func TestIDEOrphanEndedSamePort(t *testing.T) {
	f := newFakeIDE(t, "serve")
	base1, err := f.open("")
	if err != nil {
		t.Fatal(err)
	}
	orphan := f.current()
	f.disown() // the first app crashed: its lock went with it, its server stays
	// The next app: the same folder and port, no memory of the first.
	g := &ideServer{findCLI: f.findCLI, dataDir: f.dataDir, install: f.install, port: f.port, budget: f.budget}
	t.Cleanup(g.stop)
	base2, err := g.open("")
	if err != nil {
		t.Fatal(err)
	}
	if !waitClosed(orphan.done, 3*time.Second) {
		t.Fatal("the server left from before still runs")
	}
	if portOf(t, base2) != f.port || portOf(t, base1) != f.port {
		t.Fatalf("ports %d, %d; want %d", portOf(t, base1), portOf(t, base2), f.port)
	}
}

// Review 1 (P1): a second app running beside the first (an installed app and a build, two
// launches) leaves the first's live server alone and starts its own on another port.
func TestIDELiveOwnerKept(t *testing.T) {
	f := newFakeIDE(t, "serve")
	if _, err := f.open(""); err != nil {
		t.Fatal(err)
	}
	first := f.current()
	g := &ideServer{findCLI: f.findCLI, dataDir: f.dataDir, install: f.install, port: f.port, budget: f.budget}
	t.Cleanup(g.stop)
	base, err := g.open("")
	if err != nil {
		t.Fatal(err)
	}
	if closed(first.done) || waitClosed(first.done, 500*time.Millisecond) {
		t.Fatal("the other app's live server was ended")
	}
	if portOf(t, base) == f.port {
		t.Fatal("the second app took the first's port")
	}
	// The pid file still names the owner's server, not the second's.
	if rec := readRecord(t, f.data); rec.Pid != first.cmd.Process.Pid {
		t.Fatalf("pid file %+v, owner's server %d", rec, first.cmd.Process.Pid)
	}
}

// I1: a port taken by something not this app's is not ended: another port is used.
func TestIDEForeignPortLeftAlone(t *testing.T) {
	f := newFakeIDE(t, "serve")
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(f.port))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	// A pid file naming a process that is not a server of this app's (this test).
	os.WriteFile(filepath.Join(f.data, idePidFile), []byte(strconv.Itoa(os.Getpid())), 0o600)
	base, err := f.open("")
	if err != nil {
		t.Fatal(err)
	}
	if portOf(t, base) == f.port {
		t.Fatal("the taken port was used")
	}
}

// I3/Z5: one deadline for the whole start: a server that never serves, and an install that hangs,
// end within it; the install does not fail the start and is tried again on the next IDEStart.
func TestIDEOneBudget(t *testing.T) {
	old := ideProbeEvery
	ideProbeEvery = 50 * time.Millisecond
	defer func() { ideProbeEvery = old }()
	for _, mode := range []string{"silent", "downloading"} {
		t.Run(mode, func(t *testing.T) {
			f := newFakeIDE(t, mode)
			f.budget = time.Second
			start := time.Now()
			_, err := f.open("")
			if err == nil || !strings.Contains(err.Error(), "in time") {
				t.Fatalf("error: %v", err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("returned after %v (budget 1 s)", d)
			}
			if run := f.current(); run == nil || !waitClosed(run.done, 3*time.Second) {
				t.Fatal("the failed start's process still runs")
			}
		})
	}
	t.Run("install hangs", func(t *testing.T) {
		f := newFakeIDE(t, "serve")
		f.budget = 4 * time.Second // the fake's start, slow under load, fits; the hung install is cut at it
		var mu sync.Mutex
		calls := 0
		f.install = func(ctx context.Context, data string) error {
			mu.Lock()
			calls++
			n := calls
			mu.Unlock()
			if n == 1 {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		}
		start := time.Now()
		if _, err := f.open(""); err != nil {
			t.Fatalf("a start whose install hung: %v", err)
		}
		if d := time.Since(start); d > 8*time.Second {
			t.Fatalf("returned after %v (budget 4 s)", d)
		}
		if _, err := f.open(""); err != nil {
			t.Fatal(err)
		}
		for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			f.mu.Lock()
			ok := f.looked
			f.mu.Unlock()
			if ok {
				break
			}
		}
		mu.Lock()
		defer mu.Unlock()
		if calls != 2 || f.starts() != 1 {
			t.Fatalf("install tried %d times, server started %d times", calls, f.starts())
		}
	})
}

func TestIDEServes(t *testing.T) {
	for page, want := range map[string]bool{
		`<meta id="vscode-workbench-web-configuration">`:                     true,
		"The latest version of the Visual Studio Code Server is downloading": false,
		"<html>VS Code</html>": false,
		"":                     false,
	} {
		if ideServes([]byte(page)) != want {
			t.Errorf("%q: %v", page, !want)
		}
	}
}

// Review 5: ready only on a whole page with the workbench's element: an empty, foreign or cut
// body is not VS Code serving.
func TestIDEServingStrict(t *testing.T) {
	old := ideProbeEvery
	ideProbeEvery = 20 * time.Millisecond
	defer func() { ideProbeEvery = old }()
	marker := `<meta id="vscode-workbench-web-configuration" data-settings="{}">`
	for name, h := range map[string]http.HandlerFunc{
		"empty":   func(w http.ResponseWriter, r *http.Request) {},
		"foreign": func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, "<html>something else</html>") },
		"cut": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Length", "100000")
			fmt.Fprint(w, marker)
		},
		"served": func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, marker) },
	} {
		t.Run(name, func(t *testing.T) {
			srv := httptest.NewServer(h)
			defer srv.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
			defer cancel()
			err := ideServing(ctx, srv.URL+"/?tkn=x", make(chan struct{}))
			if (err == nil) != (name == "served") {
				t.Fatalf("%s: %v", name, err)
			}
		})
	}
}

// Review 4: one install at a time: the server ending while a repeat install hangs, and the new
// server's own install, do not run together.
func TestIDEOneInstallAtATime(t *testing.T) {
	f := newFakeIDE(t, "serve")
	var mu sync.Mutex
	running, most, calls := 0, 0, 0
	release := make(chan struct{})
	f.install = func(ctx context.Context, data string) error {
		mu.Lock()
		calls++
		n := calls
		running++
		most = max(most, running)
		mu.Unlock()
		defer func() { mu.Lock(); running--; mu.Unlock() }()
		switch n {
		case 1:
			return errors.New("first install failed")
		case 2: // the repeat, hanging until let go
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return nil
	}
	base, err := f.open("")
	if err != nil {
		t.Fatal(err)
	}
	f.open("") // starts the repeat install (call 2), which hangs
	for end := time.Now().Add(3 * time.Second); time.Now().Before(end); time.Sleep(10 * time.Millisecond) {
		mu.Lock()
		n := calls
		mu.Unlock()
		if n == 2 {
			break
		}
	}
	run := f.current()
	u, _ := url.Parse(base)
	http.Get("http://" + u.Host + "/exit")
	if !waitClosed(run.done, 4*time.Second) {
		t.Fatal("server did not end")
	}
	res := make(chan error, 1)
	go func() { _, err := f.open(""); res <- err }() // a new server, its own install waits for the repeat
	time.Sleep(500 * time.Millisecond)
	close(release)
	if err := <-res; err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if most != 1 || calls != 3 {
		t.Fatalf("at most %d installs at once, %d in all", most, calls)
	}
}

// Review 2.1: a server left on another port (the IDE's was taken when it started) by an app that
// crashed is ended by the next start even when the IDE's port is free again; that start takes it.
func TestIDEOrphanOnOtherPortEnded(t *testing.T) {
	f := newFakeIDE(t, "serve")
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(f.port))
	if err != nil {
		t.Fatal(err)
	}
	base1, err := f.open("")
	if err != nil {
		t.Fatal(err)
	}
	if portOf(t, base1) == f.port {
		t.Fatal("started on the taken port")
	}
	orphan := f.current()
	f.disown() // the app crashed
	l.Close()  // the IDE's port is free again
	g := &ideServer{findCLI: f.findCLI, dataDir: f.dataDir, install: f.install, port: f.port, budget: f.budget}
	t.Cleanup(g.stop)
	base2, err := g.open("")
	if err != nil {
		t.Fatal(err)
	}
	if !waitClosed(orphan.done, 3*time.Second) {
		t.Fatal("the server left on the other port still runs")
	}
	if portOf(t, base2) != f.port {
		t.Fatalf("port %d, want %d", portOf(t, base2), f.port)
	}
}

// Review 2.2: with the IDE's port taken, the server picks its own port as it listens: a port
// picked beforehand and taken by someone else before the start cannot fail it.
func TestIDEFallbackPortNotRaced(t *testing.T) {
	f := newFakeIDE(t, "serve")
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(f.port))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	var thief net.Listener
	f.ideServer.starts = func(port int) {
		if port != 0 && port != f.port { // a port picked here: someone takes it first
			thief, _ = net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
		}
	}
	defer func() {
		if thief != nil {
			thief.Close()
		}
	}()
	base, err := f.open("")
	if err != nil {
		t.Fatalf("a start on the fallback port: %v", err)
	}
	if p := portOf(t, base); p == f.port || p == 0 {
		t.Fatalf("port %d", p)
	}
}

// Review 3.1: the CLI ending before its server does (it ends in order after SIGTERM) is not the
// group's end: the server is given its time, and the restart finds the port free, the same one.
func TestIDEGroupEndsInOrder(t *testing.T) {
	f := newFakeIDE(t, "lingering")
	base1, err := f.open("")
	if err != nil {
		t.Fatal(err)
	}
	run := f.current()
	run.kill()
	if _, err := os.Stat(f.pids + ".graceful"); err != nil {
		t.Fatal("the server was not let end in order")
	}
	base2, err := f.open("")
	if err != nil {
		t.Fatal(err)
	}
	if portOf(t, base2) != portOf(t, base1) || portOf(t, base1) != f.port {
		t.Fatalf("ports %d then %d, want %d", portOf(t, base1), portOf(t, base2), f.port)
	}
}

// Review 3.2: an app quitting while its start looks for an orphan keeps its lock until that is
// done: the next app (which takes the lock only then) is never taken for its orphan.
func TestIDEStopWaitsForOrphanCheck(t *testing.T) {
	g := newFakeIDE(t, "serve")
	h := &ideServer{findCLI: g.findCLI, dataDir: g.dataDir, install: g.install, port: g.port, budget: g.budget}
	t.Cleanup(h.stop)
	var hrun *ideRun
	g.owned = func() {
		g.owned = nil
		done := make(chan struct{})
		go g.stop() // the app quitting now
		go func() {
			defer close(done)
			if _, err := h.open(""); err == nil { // the next app, at once
				h.mu.Lock()
				hrun = h.run
				h.mu.Unlock()
			}
		}()
		select {
		case <-done:
		case <-time.After(10 * time.Second):
		}
	}
	g.open("")
	if hrun == nil {
		t.Fatal("the next app did not start")
	}
	if waitClosed(hrun.done, 500*time.Millisecond) {
		t.Fatal("the next app's server was ended as an orphan")
	}
}

// Review 4.1: once the CLI has ended its address is dead (the listener was the CLI's), even while
// its server is still ending: an IDEStart then waits for that and starts the server anew.
func TestIDEOpenDuringGroupEnd(t *testing.T) {
	f := newFakeIDE(t, "lingering")
	base1, err := f.open("")
	if err != nil {
		t.Fatal(err)
	}
	run := f.current()
	syscall.Kill(run.cmd.Process.Pid, syscall.SIGTERM) // the CLI alone ends; its server lingers
	if !waitClosed(run.exited, 3*time.Second) {
		t.Fatal("the CLI did not end")
	}
	if closed(run.done) {
		t.Fatal("the group ended at once: the window under test is not there")
	}
	base2, err := f.open("")
	if err != nil {
		t.Fatal(err)
	}
	if f.starts() != 2 || !closed(run.done) {
		t.Fatalf("%d starts, old group gone: %v", f.starts(), closed(run.done))
	}
	if portOf(t, base2) != portOf(t, base1) {
		t.Fatalf("ports %d, %d", portOf(t, base1), portOf(t, base2))
	}
	if r, err := http.Get(base2); err != nil {
		t.Fatalf("the new address: %v", err)
	} else {
		r.Body.Close()
	}
}

// Review 4.2: a group that does not go (a process SIGKILL does not end) is not taken for gone: no
// new server starts beside it, the start says why, and one starts once it is gone.
func TestIDEStuckGroupRefusesStart(t *testing.T) {
	old := ideTermWait
	ideTermWait = 100 * time.Millisecond
	defer func() { ideTermWait = old; ideStuckGroup.Store(0) }()
	t.Run("own", func(t *testing.T) {
		f := newFakeIDE(t, "serve")
		base, err := f.open("")
		if err != nil {
			t.Fatal(err)
		}
		run := f.current()
		ideStuckGroup.Store(int64(run.cmd.Process.Pid))
		u, _ := url.Parse(base)
		http.Get("http://" + u.Host + "/exit")
		if !waitClosed(run.exited, 3*time.Second) {
			t.Fatal("the CLI did not end")
		}
		_, err = f.open("")
		if err == nil || !strings.Contains(err.Error(), "has not ended") || f.starts() != 1 {
			t.Fatalf("a start beside a stuck group: %v, %d starts", err, f.starts())
		}
		if closed(run.done) {
			t.Fatal("done closed while the group is there")
		}
		ideStuckGroup.Store(0)
		if !waitClosed(run.done, 6*time.Second) {
			t.Fatal("done not closed once the group went")
		}
		if _, err := f.open(""); err != nil || f.starts() != 2 {
			t.Fatalf("after: %v, %d starts", err, f.starts())
		}
	})
	t.Run("orphan", func(t *testing.T) {
		f := newFakeIDE(t, "serve")
		if _, err := f.open(""); err != nil {
			t.Fatal(err)
		}
		orphan := f.current()
		f.disown()
		ideStuckGroup.Store(int64(orphan.cmd.Process.Pid))
		g := &ideServer{findCLI: f.findCLI, dataDir: f.dataDir, install: f.install, port: f.port, budget: f.budget}
		t.Cleanup(g.stop)
		_, err := g.open("")
		if err == nil || !strings.Contains(err.Error(), "does not end") || f.starts() != 1 {
			t.Fatalf("a start beside a stuck orphan: %v, %d starts", err, f.starts())
		}
		// Review 5: asked again (its leader gone by now, the group still there): still refused.
		_, err = g.open("")
		if err == nil || !strings.Contains(err.Error(), "does not end") || f.starts() != 1 {
			t.Fatalf("a second start beside a stuck orphan: %v, %d starts", err, f.starts())
		}
		// A new app (no memory of it) refuses too: the leaderless group is the server's.
		h := &ideServer{findCLI: f.findCLI, dataDir: f.dataDir, install: f.install, port: f.port, budget: f.budget}
		t.Cleanup(h.stop)
		g.stop()
		_, err = h.open("")
		if err == nil || !strings.Contains(err.Error(), "does not end") || f.starts() != 1 {
			t.Fatalf("another app beside a stuck orphan: %v, %d starts", err, f.starts())
		}
		ideStuckGroup.Store(0)
		if _, err := h.open(""); err != nil || f.starts() != 2 {
			t.Fatalf("once it is gone: %v, %d starts", err, f.starts())
		}
	})
}
