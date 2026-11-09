package main

import (
	"strings"
	"testing"
)

// A flag that does not parse is told in the format the last --json asks for, whichever side of
// it the bad flag is (cobra stops at the bad flag: what it set by then is not the answer).
func TestLastJSONFlagDecidesAParseError(t *testing.T) {
	e := newEnv(t, inbox)
	for _, c := range []struct {
		args []string
		json bool
	}{
		{[]string{"--json", "agents", "--bogus", "--json=false"}, false},
		{[]string{"--json=false", "agents", "--bogus", "--json"}, true},
		{[]string{"agents", "--json", "--bogus"}, true},
		{[]string{"agents", "--bogus", "--json=false"}, false},
	} {
		r := e.run("", c.args...)
		if r.code != exitUsage || strings.HasPrefix(r.err, `{"error"`) != c.json {
			t.Errorf("%v: exit %d, JSON %v: %s", c.args, r.code, !c.json, r.err)
		}
	}
}
