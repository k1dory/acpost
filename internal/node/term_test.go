package node

import (
	"context"
	"strings"
	"testing"
	"time"
)

// A shell in a directory: what is typed runs there, its output comes with offsets (a reader comes
// back where it was), its end says its exit code. Not for agents or tokens.
func TestTerminal(t *testing.T) {
	n := newNode(t, inboxCfg)
	dir := t.TempDir()
	for _, p := range []Principal{{Kind: "agent", Name: "x"}, {Kind: "client", Name: "ci"}} {
		if _, err := n.OpenTerminal(ctx, p, TermRequest{Cwd: dir}); code(err) != "refused" {
			t.Fatalf("%s: %v", p.Kind, err)
		}
	}
	tm, err := n.OpenTerminal(ctx, me, TermRequest{Cwd: dir, Cols: 100, Rows: 30})
	if err != nil {
		t.Fatal(err)
	}
	if err := n.TermResize(me, tm.ID, 120, 40); err != nil {
		t.Fatal(err)
	}
	if err := n.TermInput(me, tm.ID, []byte("stty size; pwd; echo hello-\"\"term; exit 3\n")); err != nil {
		t.Fatal(err)
	}
	c, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var out strings.Builder
	var last int64
	end, err := n.TermOutput(c, me, tm.ID, 0, func(off int64, b []byte) error {
		out.Write(b)
		last = off + int64(len(b))
		return nil
	})
	if err != nil || end.Running || end.ExitCode == nil || *end.ExitCode != 3 {
		t.Fatalf("%+v %v\n%s", end, err, out.String())
	}
	s := out.String()
	if !strings.Contains(s, "hello-term") || !strings.Contains(s, "40 120") || !strings.Contains(s, strings.TrimPrefix(dir, "/private")) {
		t.Fatalf("%q", s)
	}
	// From where it was: nothing more.
	more := 0
	n.TermOutput(ctx, me, tm.ID, last, func(_ int64, b []byte) error { more += len(b); return nil })
	if more != 0 {
		t.Fatalf("%d bytes again", more)
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

// A process the shell left behind, ignoring SIGHUP and holding the terminal, does not keep the
// terminal (or the node) from ending.
func TestTerminalEndsPastALeftProcess(t *testing.T) {
	n := newNode(t, inboxCfg)
	tm, err := n.OpenTerminal(ctx, me, TermRequest{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	n.TermInput(me, tm.ID, []byte("(trap '' HUP; sleep 30 &) ; exit 0\n"))
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	end, err := n.TermOutput(c, me, tm.ID, 0, func(int64, []byte) error { return nil })
	if err != nil || end.Running || end.ExitCode == nil || *end.ExitCode != 0 {
		t.Fatalf("%+v %v", end, err)
	}
}

// Input to a program that does not read it is refused after a while, not held.
func TestTerminalInputNotHeld(t *testing.T) {
	n := newNode(t, inboxCfg)
	tm, err := n.OpenTerminal(ctx, me, TermRequest{Cwd: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	defer n.CloseTerminal(me, tm.ID)
	n.TermInput(me, tm.ID, []byte("stty -icanon; sleep 30\n"))
	time.Sleep(500 * time.Millisecond)
	start := time.Now()
	var last error
	for i := 0; i < 64 && last == nil; i++ {
		last = n.TermInput(me, tm.ID, []byte(strings.Repeat("x", 60<<10)))
	}
	if code(last) != "conflict" || time.Since(start) > 5*time.Second {
		t.Fatalf("%v after %v", last, time.Since(start))
	}
}
