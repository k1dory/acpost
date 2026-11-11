//go:build darwin

package main

import (
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// A request to show an edit is one whole file the extension takes by its name; an edit too long
// goes without its text (the file alone is shown); old requests are removed.
func TestIDERevealWritesARequest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "commands")
	now := time.Now()
	if err := ideReveal(dir, ideRequest{Folder: "/w", Path: "a.txt", Old: "two", New: "TWO", Title: "Claude"}, now); err != nil {
		t.Fatal(err)
	}
	old := filepath.Join(dir, "1-00.json")
	os.WriteFile(old, []byte("{}"), 0o600)
	os.Chtimes(old, now.Add(-time.Hour), now.Add(-time.Hour))
	if err := ideReveal(dir, ideRequest{Folder: "/w", Path: "/big", Old: strings.Repeat("x", ideRevealText), New: "y", Created: true}, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 2 {
		t.Fatalf("%d files, want the two requests", len(entries))
	}
	name := regexp.MustCompile(`^[0-9]+-[0-9a-f]+\.json$`) // reveal.js isCommand
	var got []ideRequest
	for _, e := range entries {
		if !name.MatchString(e.Name()) {
			t.Errorf("a request named %s", e.Name())
		}
		if info, _ := e.Info(); info.Mode().Perm() != 0o600 {
			t.Errorf("%s is %v", e.Name(), info.Mode().Perm())
		}
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		var r ideRequest
		if err := json.Unmarshal(b, &r); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	if got[0].Old != "two" || got[0].New != "TWO" || got[0].At != now.UnixMilli() || got[0].Title != "Claude" {
		t.Errorf("the edit: %+v", got[0])
	}
	if got[1].Old != "" || got[1].New != "" || got[1].Created || got[1].Path != "/big" {
		t.Errorf("an edit too long kept its text: %d bytes", len(got[1].Old))
	}
	if info, _ := os.Stat(dir); info.Mode().Perm() != 0o700 {
		t.Errorf("the folder is %v", info.Mode().Perm())
	}
	for _, bad := range []ideRequest{{Folder: "rel", Path: "a"}, {Folder: "/w"}} {
		if ideReveal(dir, bad, now) == nil {
			t.Errorf("took %+v", bad)
		}
	}
}

// The extension the app carries has the part that reads requests.
func TestIDEExtensionCarriesReveal(t *testing.T) {
	b, err := ideExtFS.ReadFile("ide/extension/reveal.js")
	if err != nil || !strings.Contains(string(b), "exports.plan") {
		t.Fatalf("reveal.js: %v", err)
	}
}

// The extension's own tests of what it shows for a request (ide/reveal.test.mjs).
func TestIDERevealPlan(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	if out, err := exec.Command(node, "--test", "ide/reveal.test.mjs").CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, out)
	}
}

// The extension, with VS Code played: a request the app writes for the folder the window has open
// is shown as a diff of the file, its left side the file with the edit taken back; one for another
// folder is not.
func TestIDEExtensionShowsARequest(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("no node")
	}
	root, _ := filepath.EvalSymlinks(t.TempDir())
	ext := filepath.Join(root, "server", "extensions", "acpost.acpost-ide-1.0.0")
	work := filepath.Join(root, "work")
	os.MkdirAll(filepath.Join(ext, "node_modules", "vscode"), 0o700)
	os.MkdirAll(work, 0o700)
	for _, f := range []string{"extension.js", "reveal.js"} {
		b, _ := ideExtFS.ReadFile("ide/extension/" + f)
		os.WriteFile(filepath.Join(ext, f), b, 0o600)
	}
	os.WriteFile(filepath.Join(work, "a.txt"), []byte("one\nTWO\nthree\n"), 0o600)
	os.WriteFile(filepath.Join(ext, "node_modules", "vscode", "index.js"), []byte(`
exports.shown = []; exports.providers = {};
exports.EventEmitter = class { constructor() { this.event = () => {}; } fire() {} dispose() {} };
exports.Uri = { file: p => ({ path: p, toString: () => "file://" + p }), from: o => ({ ...o, toString: () => o.scheme + ":" + o.path }) };
exports.workspace = { workspaceFolders: [{ uri: { fsPath: process.env.WORK } }],
  registerTextDocumentContentProvider: (s, p) => { exports.providers[s] = p; return { dispose() {} }; } };
exports.window = { showTextDocument: async u => { exports.shown.push({ open: u.path }); } };
exports.commands = { executeCommand: async (c, left, right, title) => {
  if (c === "vscode.diff") exports.shown.push({ diff: right.path, before: exports.providers[left.scheme].provideTextDocumentContent(left), title });
} };`), 0o600)
	os.WriteFile(filepath.Join(ext, "t.cjs"), []byte(`const assert = require("node:assert/strict");
const vscode = require("vscode"), ext = require("./extension.js");
const subs = [], ctx = { extensionPath: __dirname, subscriptions: subs, workspaceState: { get: () => true, update: async () => {} } };
(async () => {
  await ext.activate(ctx);
  console.log("ready");
  for (let i = 0; i < 100 && vscode.shown.length < 1; i++) await new Promise(r => setTimeout(r, 50));
  await new Promise(r => setTimeout(r, 300)); // the other folder's request would have come by now
  assert.deepEqual(vscode.shown, [{ diff: process.env.WORK + "/a.txt", before: "one\ntwo\nthree\n", title: "a.txt (Claude)" }]);
  for (const s of subs) s.dispose();
  console.log("ok");
})().catch((e) => { console.error(e); process.exit(1); });
`), 0o600)
	cmd := exec.Command(node, "t.cjs")
	cmd.Dir = ext
	cmd.Env = append(os.Environ(), "WORK="+work)
	stdout, _ := cmd.StdoutPipe()
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 6)
	if _, err := io.ReadFull(stdout, line); err != nil || string(line) != "ready\n" { // it watches the folder now
		cmd.Wait()
		t.Fatalf("the extension did not start: %q %v %s", line, err, stderr.String())
	}
	dir := filepath.Join(root, "commands")
	if err := ideReveal(dir, ideRequest{Folder: "/elsewhere", Path: filepath.Join(work, "a.txt"), Old: "two", New: "TWO"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := ideReveal(dir, ideRequest{Folder: work, Path: "a.txt", Old: "two", New: "TWO", Title: "Claude"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(stdout)
	if err := cmd.Wait(); err != nil || !strings.Contains(string(rest), "ok") {
		t.Fatalf("%v: %s%s", err, rest, stderr.String())
	}
}
