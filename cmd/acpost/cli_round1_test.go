package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// Wrong arguments are usage errors (exit 2, code invalid) whatever cobra's wording; --json is
// the last flag given, over ACPOST_JSON, before flags are parsed too.
func TestUsageErrorsAndJSONFlag(t *testing.T) {
	e := newEnv(t, inbox)
	for _, args := range [][]string{{"wait"}, {"show", "a", "b"}, {"nosuch"}, {"forge", "log"}, {"status", "extra"}} {
		r := e.run("", append([]string{"--json"}, args...)...)
		var out struct{ Error struct{ Code string } }
		if r.code != exitUsage || json.Unmarshal([]byte(r.err), &out) != nil || out.Error.Code != "invalid" {
			t.Fatalf("%v: exit %d %s", args, r.code, r.err)
		}
	}
	// --json=1 is JSON from the start: a flag that does not parse is told in JSON.
	if r := e.run("", "--json=1", "agents", "--bogus"); r.code != exitUsage || !strings.HasPrefix(r.err, `{"error"`) {
		t.Fatalf("--json=1: exit %d %s", r.code, r.err)
	}
	// --json=false wins over ACPOST_JSON=1.
	e.vars = append(e.vars, "ACPOST_JSON=1")
	if r := e.run("", "--json=false", "--node", "http://127.0.0.1:1", "agents"); r.code != exitNoNode || strings.HasPrefix(r.err, "{") {
		t.Fatalf("--json=false: exit %d %s", r.code, r.err)
	}
	if r := e.run("", "--node", "http://127.0.0.1:1", "agents"); r.code != exitNoNode || !strings.HasPrefix(r.err, `{"error"`) {
		t.Fatalf("ACPOST_JSON=1: exit %d %s", r.code, r.err)
	}
}
