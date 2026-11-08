//go:build darwin

package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/config"
)

// ownNode points the app at a configuration whose socket nobody listens on, and stubs the
// node the app runs itself: it counts the starts and how many still run.
func ownNode(t *testing.T, run func(ctx context.Context, ready func()) error) {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("ACPOST_NODE", "")
	t.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state"))
	cfg := filepath.Join(dir, "config.yaml")
	sock, _ := os.MkdirTemp("/tmp", "acp")
	t.Cleanup(func() { os.RemoveAll(sock) })
	os.WriteFile(cfg, []byte("node:\n  listen: [\"unix://"+sock+"/n.sock\"]\n"), 0o600)
	t.Setenv("ACPOST_CONFIG", cfg)
	old := runNode
	runNode = func(ctx context.Context, _ *config.Config, _ *slog.Logger, ready func()) error {
		return run(ctx, ready)
	}
	t.Cleanup(func() { runNode = old })
}

// Ten Status calls at once while the node is down start one node, and shutting the app down
// stops it: a second start used to take the first one's cancel with it.
func TestStatusStartsOneNode(t *testing.T) {
	var starts, running atomic.Int32
	ownNode(t, func(ctx context.Context, ready func()) error {
		starts.Add(1)
		running.Add(1)
		defer running.Add(-1)
		time.Sleep(50 * time.Millisecond) // a node takes a while to listen
		ready()
		<-ctx.Done()
		return nil
	})
	n := &Node{emitFn: func(string, any) {}}
	n.startErr = "the node did not start" // as after a failed start
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() { defer wg.Done(); n.Status() }()
	}
	wg.Wait()
	if s := starts.Load(); s != 1 {
		t.Errorf("%d nodes started, want 1", s)
	}
	n.ServiceShutdown()
	eventually(t, 2*time.Second, "shutdown stops every node started", func() bool { return running.Load() == 0 })
}

// A start that does not get ready in time is cancelled, not left starting in the background
// (the next Status would start another beside it).
func TestNodeStartTimeoutCancels(t *testing.T) {
	var running atomic.Int32
	ownNode(t, func(ctx context.Context, ready func()) error {
		running.Add(1)
		defer running.Add(-1)
		<-ctx.Done()
		return ctx.Err()
	})
	old := nodeStartWait
	nodeStartWait = 100 * time.Millisecond
	t.Cleanup(func() { nodeStartWait = old })
	n := &Node{emitFn: func(string, any) {}}
	if err := n.ensureNode(); err == nil {
		t.Fatal("a node that never got ready: no error")
	}
	eventually(t, 2*time.Second, "the timed-out start is cancelled", func() bool { return running.Load() == 0 })
}
