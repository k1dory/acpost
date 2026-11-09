package main

import (
	"strings"
	"testing"
)

// The pre-scan knows the flags of the command named, not of any: send's --wait takes no value
// (recv's takes a time), so the --json after it is a flag.
func TestJSONScanUsesTheCommandsFlags(t *testing.T) {
	e := newEnv(t, inbox)
	e.vars = append(e.vars, "ACPOST_JSON=0")
	for _, args := range [][]string{
		{"send", "--wait", "--json", "--bogus"},
		{"message", "send", "--wait", "--json", "--bogus"},
		{"--node", "unix:///nowhere", "send", "--wait", "--json", "--bogus"},
	} {
		if r := e.run("", args...); r.code != exitUsage || !strings.HasPrefix(r.err, `{"error"`) {
			t.Errorf("%v: exit %d: %s", args, r.code, r.err)
		}
	}
	// recv --wait takes a time: what follows is that, and --json=false is not read as a flag.
	if r := e.run("", "--json", "recv", "--wait", "--json=false", "--bogus"); !strings.HasPrefix(r.err, `{"error"`) {
		t.Errorf("recv --wait: %s", r.err)
	}
}
