package node

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A shell on a pseudo-console: what is typed runs, what it shows is read, its end says its exit
// code, and the terminal is then closed and forgotten.
func TestTerminal(t *testing.T) {
	n := newNode(t, inboxCfg)
	tm, err := n.OpenTerminal(ctx, me, TermRequest{Cwd: t.TempDir(), Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.TermResize(me, tm.ID, 120, 40); err != nil {
		t.Fatal(err)
	}
	// The words are joined by the shell: seen whole only in what it prints, not in what is typed.
	if err := n.TermInput(me, tm.ID, []byte("echo ('hello-' + 'term'); exit 3\r")); err != nil {
		t.Fatal(err)
	}
	c, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var out strings.Builder
	end, err := n.TermOutput(c, me, tm.ID, 0, func(_ int64, b []byte) error { out.Write(b); return nil })
	if err != nil || end.Running || end.ExitCode == nil || *end.ExitCode != 3 {
		t.Fatalf("%+v %v\n%q", end, err, out.String())
	}
	if !strings.Contains(out.String(), "hello-term") {
		t.Fatalf("%q", out.String())
	}
	if err := n.TermInput(me, tm.ID, []byte("x")); code(err) != "conflict" {
		t.Fatalf("typing into an ended shell: %v", err)
	}
	if err := n.CloseTerminal(me, tm.ID); err != nil {
		t.Fatal(err)
	}
	if ts, _ := n.Terminals(me); len(ts) != 0 {
		t.Fatalf("%+v", ts)
	}
}

// A terminal closed while its shell runs ends the shell.
func TestTerminalClosedEndsItsShell(t *testing.T) {
	n := newNode(t, inboxCfg)
	tm, err := n.OpenTerminal(ctx, me, TermRequest{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.CloseTerminal(me, tm.ID); err != nil {
		t.Fatal(err)
	}
	for end := time.Now().Add(20 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		ts, _ := n.Terminals(me)
		if len(ts) == 0 {
			return
		}
		if time.Now().After(end) {
			t.Fatalf("still there: %+v", ts)
		}
	}
}
