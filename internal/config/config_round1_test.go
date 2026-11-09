package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A negative limit, an empty token entry or a size past int64 is refused, naming the key.
func TestInvalidLimitsAndTokens(t *testing.T) {
	for in, want := range map[string]string{
		"limits: {max_depth: -1}":                   "limits.max_depth",
		"limits: {max_attempts: -1}":                "limits.max_attempts",
		"limits: {max_queued_per_agent: -5}":        "limits.max_queued_per_agent",
		"limits: {max_events_per_message: -1}":      "limits.max_events_per_message",
		"tokens: [null]":                            "tokens[0]",
		"limits: {max_reply: 17179869183GiB}":       "17179869183GiB",
		"limits: {max_store: 99999999999GiB}":       "99999999999GiB",
		"limits: {max_body: 9223372036854775807KB}": "9223372036854775807KB",
	} {
		_, err := Parse([]byte(in), ".yaml")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v, want an error naming %s", in, err, want)
		}
	}
	// The largest that fits still does, and 0 is still the default.
	c, err := Parse([]byte("limits: {max_store: 8589934591GiB, max_depth: 0}"), ".yaml")
	if err != nil || int64(c.Limits.MaxStore) != 8589934591<<30 || c.Limits.MaxDepth != 4 {
		t.Fatalf("%+v %v", c, err)
	}
}

// A file named by --config or ACPOST_CONFIG that does not exist is an error naming it; only the
// default file may be absent.
func TestLoadNamedMissingFile(t *testing.T) {
	t.Setenv("ACPOST_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	missing := filepath.Join(t.TempDir(), "cofnig.yaml")
	if _, err := Load(missing); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("named missing file: %v", err)
	}
	if c, err := Load(DefaultPath()); err != nil || c.Limits.MaxBody == 0 {
		t.Fatalf("absent default file: %+v %v", c, err)
	}
	t.Setenv("ACPOST_CONFIG", missing)
	if _, err := Load(DefaultPath()); err == nil || !strings.Contains(err.Error(), missing) {
		t.Fatalf("ACPOST_CONFIG names a missing file: %v", err)
	}
	if err := os.WriteFile(missing, []byte("retention: 1h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err := Load(DefaultPath()); err != nil || c.Retention.D().Hours() != 1 {
		t.Fatalf("ACPOST_CONFIG file present: %+v %v", c, err)
	}
}

// The default file named on purpose must exist too: by ACPOST_CONFIG (Load sees it) or by
// --config (the CLI calls LoadNamed).
func TestNamedDefaultFileMustExist(t *testing.T) {
	t.Setenv("ACPOST_CONFIG", "")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	def := DefaultPath()
	if _, err := LoadNamed(def); err == nil || !strings.Contains(err.Error(), def) {
		t.Fatalf("--config naming the absent default file: %v", err)
	}
	t.Setenv("ACPOST_CONFIG", def)
	if _, err := Load(DefaultPath()); err == nil || !strings.Contains(err.Error(), def) {
		t.Fatalf("ACPOST_CONFIG naming the absent default file: %v", err)
	}
	t.Setenv("ACPOST_CONFIG", "")
	if c, err := Load(def); err != nil || c.Limits.MaxBody == 0 {
		t.Fatalf("the implicit default file absent: %+v %v", c, err)
	}
}
