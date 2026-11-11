package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

// shortDir is a directory short enough for a Unix socket path (104 bytes on macOS).
func shortDir(t *testing.T) string {
	t.Helper()
	root := "/tmp"
	if runtime.GOOS == "windows" {
		root = "" // the system's temporary directory
	}
	dir, err := os.MkdirTemp(root, "acps")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func testConfig(t *testing.T, dir string, listen ...string) *config.Config {
	t.Helper()
	y := fmt.Sprintf("node:\n  name: srv%d\n  listen: [%s]\nstore: sqlite://%s/s/store.db\nagents:\n  bot: {driver: inbox}\n",
		time.Now().UnixNano()%1e6, strings.Join(listen, ", "), dir)
	cfg, err := config.Parse([]byte(y), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// runServer runs Run in the background and waits until it serves; the returned func stops it and
// gives Run's error and how long it took to return.
func runServer(t *testing.T, cfg *config.Config) (stop func() (error, time.Duration)) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	up := make(chan struct{})
	res := make(chan error, 1)
	go func() { res <- Run(ctx, cfg, quiet, func() { close(up) }) }()
	select {
	case <-up:
	case err := <-res:
		cancel()
		t.Fatalf("Run: %v", err)
	case <-time.After(10 * time.Second):
		cancel()
		t.Fatal("Run did not come up")
	}
	var once sync.Once
	var err error
	var took time.Duration
	stop = func() (error, time.Duration) {
		once.Do(func() {
			start := time.Now()
			cancel()
			select {
			case err = <-res:
			case <-time.After(15 * time.Second):
				err = errors.New("Run did not return")
			}
			took = time.Since(start)
		})
		return err, took
	}
	t.Cleanup(func() { stop() })
	return stop
}

// The second listener fails to bind: the first serves nothing afterwards, its socket and the pid
// file are gone, and a second Run in the same process (the desktop's restart) comes up.
func TestRunRollsBackWhenALaterListenerFails(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "s", "a.sock")
	busy, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg := testConfig(t, dir, "unix://"+sock, "tcp://"+busy.Addr().String())
	if err := Run(context.Background(), cfg, quiet, func() { t.Error("ready after a failed bind") }); err == nil {
		t.Fatal("Run with an occupied port returned nil")
	}
	if _, err := os.Lstat(sock); !os.IsNotExist(err) {
		t.Errorf("socket left behind: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "s", "acpost.pid")); !os.IsNotExist(err) {
		t.Errorf("pid file left behind: %v", err)
	}
	if c, err := net.DialTimeout("unix", sock, time.Second); err == nil {
		c.Close()
		t.Fatal("the first listener still serves after Run failed")
	}

	busy.Close()
	stop := runServer(t, cfg)
	c, err := client.New("unix://"+sock, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Status(context.Background()); err != nil {
		t.Fatalf("second Run does not serve: %v", err)
	}
	if err, _ := stop(); err != nil {
		t.Fatalf("stop: %v", err)
	}
}

// A TLS key pair that does not load fails Run the same clean way.
func TestRunRollsBackOnBadTLS(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "s", "a.sock")
	cfg := testConfig(t, dir, "unix://"+sock, "tcp://127.0.0.1:0")
	cfg.Node.TLS = &config.TLS{Cert: filepath.Join(dir, "none.pem"), Key: filepath.Join(dir, "none.key")}
	if err := Run(context.Background(), cfg, quiet, nil); err == nil || !strings.Contains(err.Error(), "node.tls") {
		t.Fatalf("Run with a missing key pair: %v", err)
	}
	if _, err := os.Lstat(sock); !os.IsNotExist(err) {
		t.Errorf("socket left behind: %v", err)
	}
}

// Stopping ends the requests in flight: a long poll does not hold the stop for its grace.
func TestRunStopEndsLongPolls(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "s", "a.sock")
	free, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := free.Addr().String()
	free.Close()
	sum := sha256.Sum256([]byte("peer-secret"))
	y := fmt.Sprintf("node:\n  name: stop1\n  listen: [unix://%s, tcp://%s]\nstore: sqlite://%s/s/store.db\n"+
		"agents:\n  bot: {driver: inbox}\n  idle: {driver: inbox}\ntokens:\n  - {name: peer, sha256: %s, node: true}\n",
		sock, addr, dir, hex.EncodeToString(sum[:]))
	cfg, err := config.Parse([]byte(y), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	stop := runServer(t, cfg)
	c, err := client.New("unix://"+sock, "", "")
	if err != nil {
		t.Fatal(err)
	}
	peer, err := client.New("http://"+addr, "peer-secret", "")
	if err != nil {
		t.Fatal(err)
	}
	m, _, err := c.Send(context.Background(), client.SendRequest{To: "agent:bot", Body: "x"}, "")
	if err != nil {
		t.Fatal(err)
	}
	waiting := make(chan error, 4)
	// A claim with nothing to take, and a joined node's claim (it announced no agents): cut by the
	// stop, they are not "nothing came", which a consumer takes as a reason to stop asking.
	go func() {
		_, got, err := c.Claim(context.Background(), "idle", 60*time.Second, time.Minute)
		if got {
			err = errors.New("claimed a message")
		}
		waiting <- err
	}()
	go func() {
		_, got, err := peer.NodeClaim(context.Background(), "peer", 60*time.Second, time.Minute)
		if got {
			err = errors.New("claimed a message")
		}
		waiting <- err
	}()
	go func() {
		_, err := c.Wait(context.Background(), m.ID, 60*time.Second)
		waiting <- err
	}()
	go func() {
		_, err := c.WaitFinal(context.Background(), m.ID, 0)
		waiting <- err
	}()
	time.Sleep(300 * time.Millisecond) // the waits are in their handlers
	err, took := stop()
	if err != nil {
		t.Fatalf("stop: %v", err)
	}
	if took > 2*time.Second {
		t.Fatalf("stop with an open wait took %v", took)
	}
	// Cut by the stop, a wait is what a refused connection is: unavailable, worth asking again.
	for range 4 {
		select {
		case err := <-waiting:
			if !errors.Is(err, client.ErrUnavailable) {
				t.Fatalf("a wait or claim cut by the stop: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("the wait did not end with the node")
		}
	}
	// Asked again of the node started again, the same wait is answered.
	runServer(t, cfg)
	got, err := c.Wait(context.Background(), m.ID, 200*time.Millisecond)
	if err != nil || got.ID != m.ID {
		t.Fatalf("the wait again: %+v %v", got, err)
	}
}

type failingListener struct {
	net.Listener
	fail chan struct{}
}

func (l failingListener) Accept() (net.Conn, error) {
	<-l.fail
	return nil, errors.New("accept: the network went away")
}

// A listener that fails makes Run return its error: a supervisor sees a failure, not a clean exit.
func TestRunReturnsListenerFailure(t *testing.T) {
	dir := shortDir(t)
	fail := make(chan struct{})
	old := wrapListener
	wrapListener = func(ln net.Listener) net.Listener { return failingListener{ln, fail} }
	t.Cleanup(func() { wrapListener = old })

	cfg := testConfig(t, dir, "unix://"+filepath.Join(dir, "s", "a.sock"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	up := make(chan struct{})
	res := make(chan error, 1)
	go func() { res <- Run(ctx, cfg, quiet, func() { close(up) }) }()
	<-up
	close(fail)
	select {
	case err := <-res:
		if err == nil || !strings.Contains(err.Error(), "the network went away") {
			t.Fatalf("Run after a listener failure: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its listener failed")
	}
}

// A path that is a regular file is not taken for a stale socket and removed.
func TestListenUnixKeepsAFileThatIsNotASocket(t *testing.T) {
	dir := shortDir(t)
	path := filepath.Join(dir, "a.sock")
	if err := os.WriteFile(path, []byte("mine"), 0o600); err != nil {
		t.Fatal(err)
	}
	if ln, err := ListenUnix(path); err == nil {
		ln.Close()
		t.Fatal("ListenUnix replaced a regular file")
	}
	if b, err := os.ReadFile(path); err != nil || string(b) != "mine" {
		t.Fatalf("the file is gone: %q %v", b, err)
	}

	// A stale socket (nobody listening) is still replaced.
	stale := filepath.Join(dir, "b.sock")
	ln, err := net.Listen("unix", stale)
	if err != nil {
		t.Fatal(err)
	}
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	ln.Close()
	ln, err = ListenUnix(stale)
	if err != nil {
		t.Fatalf("stale socket: %v", err)
	}
	ln.Close()
}

// Run's own context ending by a deadline stops it as a cancel does: what was cut is unavailable to
// its client (not an internal error), and a wait that simply ran out is a normal answer.
func TestRunStopByDeadlineIsUnavailable(t *testing.T) {
	dir := shortDir(t)
	sock := filepath.Join(dir, "s", "a.sock")
	cfg := testConfig(t, dir, "unix://"+sock)
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	up := make(chan struct{})
	res := make(chan error, 1)
	go func() { res <- Run(ctx, cfg, quiet, func() { close(up) }) }()
	<-up
	c, _ := client.New("unix://"+sock, "", "")
	m, _, err := c.Send(context.Background(), client.SendRequest{To: "agent:bot", Body: "x"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := c.Wait(context.Background(), m.ID, 100*time.Millisecond); err != nil || got.ID != m.ID {
		t.Fatalf("a wait that ran out: %+v %v", got, err)
	}
	if _, err := c.Wait(context.Background(), m.ID, 60*time.Second); !errors.Is(err, client.ErrUnavailable) {
		t.Fatalf("a wait cut by Run's deadline: %v", err)
	}
	select {
	case err := <-res:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return")
	}
}
