package main

import (
	"strings"
	"testing"
)

// The JSON pre-scan reads flags, not their values: a body that says --json=false is a body.
func TestJSONScanSkipsFlagValues(t *testing.T) {
	e := newEnv(t, inbox)
	for _, args := range [][]string{
		{"--json", "send", "--to", "bot", "--body", "--json=false", "--bogus"},
		{"--json", "send", "--to", "bot", "-a", "--json=false", "--bogus"},
	} {
		if r := e.run("", args...); r.code != exitUsage || !strings.HasPrefix(r.err, `{"error"`) {
			t.Errorf("%v: exit %d: %s", args, r.code, r.err)
		}
	}
	if r := e.run("", "send", "--to", "bot", "--json", "--bogus", "--", "--json=false"); !strings.HasPrefix(r.err, `{"error"`) {
		t.Errorf("after --: %s", r.err)
	}
}
