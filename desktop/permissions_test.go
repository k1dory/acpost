//go:build darwin

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPermissionEditKeepsTheRestOfTheFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.local.json")
	orig := `{"z": 1, "permissions": {"deny": ["Read(./.env)"], "allow": ["Bash(ls:*)"]}, "a": {"b": [1, 2]}}`
	if err := os.WriteFile(p, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := editPermList(p, "allow", "mcp__forgejo__search_repos", true); err != nil {
		t.Fatal(err)
	}
	if err := editPermList(p, "allow", "mcp__forgejo__search_repos", true); err != nil { // twice: once
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	want := `{
  "z": 1,
  "permissions": {
    "deny": [
      "Read(./.env)"
    ],
    "allow": [
      "Bash(ls:*)",
      "mcp__forgejo__search_repos"
    ]
  },
  "a": {
    "b": [
      1,
      2
    ]
  }
}
`
	if string(got) != want {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
	if st, _ := os.Stat(p); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v: the file's own is kept", st.Mode().Perm())
	}
	// Removed: an emptied list goes, and an emptied permissions too.
	for _, r := range [][2]string{{"allow", "Bash(ls:*)"}, {"allow", "mcp__forgejo__search_repos"}, {"deny", "Read(./.env)"}} {
		if err := editPermList(p, r[0], r[1], false); err != nil {
			t.Fatal(err)
		}
	}
	got, _ = os.ReadFile(p)
	if want := "{\n  \"z\": 1,\n  \"a\": {\n    \"b\": [\n      1,\n      2\n    ]\n  }\n}\n"; string(got) != want {
		t.Fatalf("got\n%s", got)
	}
}

func TestPermissionEditMakesTheFileAndLeavesBadJSON(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".claude", "settings.local.json")
	if err := editPermList(p, "deny", "Bash(rm:*)", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatal("removing from no file makes none")
	}
	if err := editPermList(p, "deny", "Bash(rm:*)", true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if want := "{\n  \"permissions\": {\n    \"deny\": [\n      \"Bash(rm:*)\"\n    ]\n  }\n}\n"; string(got) != want {
		t.Fatalf("got\n%s", got)
	}
	bad := filepath.Join(dir, "bad.json")
	os.WriteFile(bad, []byte(`{"permissions": {"allow": "x"}}`), 0o644)
	if err := editPermList(bad, "allow", "y", true); err == nil {
		t.Fatal("a list that is not a list is not rewritten")
	}
	os.WriteFile(bad, []byte(`{ // comment`), 0o644)
	if err := editPermList(bad, "allow", "y", true); err == nil {
		t.Fatal("not JSON: not rewritten")
	}
	if b, _ := os.ReadFile(bad); string(b) != `{ // comment` {
		t.Fatal("the file changed")
	}
}

func TestPermissionRulesReadsEachFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	proj := filepath.Join(t.TempDir(), "proj")
	os.MkdirAll(filepath.Join(proj, ".claude"), 0o755)
	os.MkdirAll(filepath.Join(home, ".claude"), 0o755)
	os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"permissions":{"allow":["WebSearch"],"defaultMode":"acceptEdits"}}`), 0o644)
	os.WriteFile(filepath.Join(proj, ".claude", "settings.local.json"), []byte(`{"permissions":{"additionalDirectories":["../lib"]}}`), 0o644)
	got, err := (&Node{}).PermissionRules(proj)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Source != "user" || got[1].Source != "project" || got[2].Source != "local" {
		t.Fatalf("%+v", got)
	}
	if got[0].Allow[0] != "WebSearch" || got[0].Mode != "acceptEdits" || got[1].Exists || got[2].Dirs[0] != "../lib" {
		t.Fatalf("%+v", got)
	}
	// Home: its .claude is the person's own, no project of its own.
	got, _ = (&Node{}).PermissionRules(home)
	if len(got) != 2 || got[1].Source != "local" {
		t.Fatalf("%+v", got)
	}
	if err := (&Node{}).PermissionEdit(proj, "local", "nope", "x", true); err == nil {
		t.Fatal("a list that is none")
	}
	if err := (&Node{}).PermissionEdit(proj, "local", "allow", "a\nb", true); err == nil {
		t.Fatal("a rule of two lines")
	}
}

// A file with anything after its object (a comment) is not JSON: left byte for byte.
func TestPermissionEditLeavesTrailingText(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.json")
	orig := `{"env":{"KEEP":"yes"}} // important notes`
	os.WriteFile(p, []byte(orig), 0o644)
	if err := editPermList(p, "allow", "WebSearch", true); err == nil {
		t.Fatal("rewritten")
	}
	if b, _ := os.ReadFile(p); string(b) != orig {
		t.Fatalf("changed: %s", b)
	}
}

// A file changed between the read and the write (Claude Code writing its settings) is read and
// edited again: what was written meanwhile stays, and the rule is added to it.
func TestPermissionEditKeepsAChangeMadeMeanwhile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "settings.local.json")
	os.WriteFile(p, []byte(`{"permissions":{"allow":["A"]}}`), 0o600)
	changed := false
	permBeforeWrite = func() {
		if !changed {
			changed = true
			os.WriteFile(p, []byte(`{"model":"opus","permissions":{"allow":["A","B"]}}`), 0o600)
		}
	}
	defer func() { permBeforeWrite = nil }()
	if err := editPermList(p, "allow", "C", true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	want := "{\n  \"model\": \"opus\",\n  \"permissions\": {\n    \"allow\": [\n      \"A\",\n      \"B\",\n      \"C\"\n    ]\n  }\n}\n"
	if string(got) != want {
		t.Fatalf("got\n%s", got)
	}
}

// A rule saved for this Mac only keeps its file out of the project's commits.
func TestPermissionEditLocalIsNotCommitted(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	t.Setenv("HOME", t.TempDir())
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	sub := filepath.Join(repo, "app", "[tenant] *")
	os.MkdirAll(sub, 0o755)
	if err := (&Node{}).PermissionEdit(sub, "local", "allow", "Bash(ls:*)", true); err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", repo, "check-ignore", "-q", "app/[tenant] */.claude/settings.local.json").Run(); err != nil {
		t.Fatal("the local settings would be committed")
	}
	// Again: not added twice.
	(&Node{}).PermissionEdit(sub, "local", "allow", "Bash(pwd:*)", true)
	b, _ := os.ReadFile(filepath.Join(repo, ".git", "info", "exclude"))
	if n := strings.Count(string(b), "settings.local.json"); n != 1 {
		t.Fatalf("excluded %d times:\n%s", n, b)
	}
	// On another node: its settings are that machine's, not this Mac's.
	t.Setenv("ACPOST_NODE", "https://box:8765")
	if _, err := (&Node{}).PermissionRules(sub); err == nil {
		t.Fatal("another node's settings read here")
	}
}

// Claude Code's own configuration elsewhere (CLAUDE_CONFIG_DIR): the person's settings are there.
func TestPermissionRulesFollowClaudeConfigDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	cfg := filepath.Join(t.TempDir(), "claude-work")
	t.Setenv("CLAUDE_CONFIG_DIR", cfg)
	got, err := (&Node{}).PermissionRules(t.TempDir())
	if err != nil || got[0].Path != filepath.Join(cfg, "settings.json") {
		t.Fatalf("%+v %v", got, err)
	}
}
