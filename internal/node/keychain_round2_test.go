package node

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/k1dory/acpost/internal/acptest"
)

// noKeychain keeps a test away from the user's secrets: a temporary home, the file store, and a
// `security` first in PATH that marks being run; at the end the mark must not be there (a store
// that still reached the keychain fails the test instead of prompting or reading real tokens).
func noKeychain(t *testing.T) {
	t.Helper()
	home, bin := t.TempDir(), t.TempDir()
	mark := filepath.Join(home, "security-ran")
	script := "#!/bin/sh\n: > '" + mark + "'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "security"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	acptest.SetHome(t.Setenv, home)
	t.Setenv("ACPOST_SECRETS_FILE", "1")
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Cleanup(func() {
		if _, err := os.Stat(mark); err == nil {
			t.Error("the test reached the keychain (security ran)")
		}
	})
}
