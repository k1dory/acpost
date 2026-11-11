//go:build unix

package node

import (
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

func TestACPReplacementCleansOldProcessGroup(t *testing.T) {
	n := acpNode(t, "preamble: false")
	reply := ask(t, n, SendRequest{
		Body: "shell sleep 60 </dev/null >/dev/null 2>&1 & echo $!",
	}).Reply
	child, err := strconv.Atoi(strings.TrimSpace(reply))
	if err != nil {
		t.Fatal(reply, err)
	}
	t.Cleanup(func() { _ = syscall.Kill(child, syscall.SIGKILL) })

	slot := n.acp["fake"].slots[0]
	slot.mu.Lock()
	old := slot.proc
	slot.mu.Unlock()
	if err := old.cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	<-old.done

	if _, err := slot.ensureProc(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if syscall.Kill(child, 0) == syscall.ESRCH {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("descendant survived replacement of its exited ACP parent")
}

func TestACPTurnTimeoutInterruptsBlockedWrite(t *testing.T) {
	n := acpNode(t, "preamble: false\nturn_timeout: 20ms")
	slot := n.acp["fake"].slots[0]
	p, err := slot.ensureProc(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := slot.session(ctx, p, "task:T"); err != nil {
		t.Fatal(err)
	}

	if err := syscall.Kill(p.cmd.Process.Pid, syscall.SIGSTOP); err != nil {
		t.Fatal(err)
	}
	defer syscall.Kill(p.cmd.Process.Pid, syscall.SIGCONT)

	m, _, err := n.Send(ctx, me, SendRequest{
		To: "fake", Task: "T", Body: strings.Repeat("x", 1<<20),
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := n.Wait(ctx, me, m.ID, time.Second)
	if err != nil || got.State != store.Failed ||
		!strings.Contains(got.Error, "timeout") {
		t.Fatalf("turn timeout did not stop blocked write: state=%s error=%q err=%v",
			got.State, got.Error, err)
	}
}
