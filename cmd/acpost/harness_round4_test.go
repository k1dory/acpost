package main

import (
	"os"
	"path/filepath"
	"testing"
)

// answers says whether the env's node answers on its socket. The socket is named (--node): a
// status without it would autostart a second node on the same store while the one serve() started
// is still opening it, and one of the two then exits with "the store is in use by another node".
func (e *env) answers() bool {
	return e.run("", "--node", "unix://"+e.sock, "status").code == 0
}

// The check serve() waits with starts nothing by itself: asked before any node is up, none is.
func TestReadinessCheckStartsNoNode(t *testing.T) {
	e := newEnv(t, inbox)
	if e.answers() {
		t.Fatal("a node answers where none was started")
	}
	for _, f := range []string{"acpost.pid", "acpost.log"} {
		if _, err := os.Stat(filepath.Join(filepath.Dir(e.sock), f)); err == nil {
			t.Fatalf("the readiness check started a node (%s is there)", f)
		}
	}
}
