package main

import (
	"os"
	"path/filepath"
	"testing"
)

// A send a flag refuses does nothing first: no node started, no file read; a negative duration is
// a usage error, not "no limit".
func TestFlagsCheckedBeforeAnything(t *testing.T) {
	e := newEnv(t, inbox)
	pid := filepath.Join(filepath.Dir(e.sock), "acpost.pid")
	for _, args := range [][]string{
		{"send", "--to", "bot", "--body", "x", "--meta", "nokv"},
		{"send", "--to", "bot", "--body", "x", "--wait", "--timeout", "-5s"},
		{"send", "--to", "bot", "--body", "x", "--ttl", "-1m"},
		{"wait", "msg_1", "--timeout", "-1s"},
		{"recv", "--as", "bot", "--wait", "-1s"},
	} {
		r := e.run("", args...)
		if r.code != exitUsage {
			t.Fatalf("%v: exit %d (%s)", args, r.code, r.err)
		}
		if _, err := os.Stat(pid); err == nil {
			t.Fatalf("%v started a node before refusing its flags", args)
		}
	}
}
