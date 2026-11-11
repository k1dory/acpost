//go:build darwin

package main

import (
	"encoding/json"
	"fmt"
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
	os.WriteFile(filepath.Join(work, "dirty.txt"), []byte("one\nTWO\nthree\n"), 0o600)
	os.WriteFile(filepath.Join(ext, "node_modules", "vscode", "index.js"), []byte(`
exports.shown = []; exports.providers = {};
exports.EventEmitter = class { constructor() { this.event = () => {}; } fire() {} dispose() {} };
exports.Uri = { file: p => ({ path: p, toString: () => "file://" + p }), from: o => ({ ...o, toString: () => o.scheme + ":" + o.path }) };
// The editor's documents: dirty.txt has a line the user has not saved; the others are open and
// unchanged, and have not read the agent's write yet (their text is the old one).
exports.workspace = { workspaceFolders: [{ uri: { fsPath: process.env.WORK } }],
  openTextDocument: async u => u.path.endsWith("dirty.txt")
    ? { isDirty: true, getText: () => "unsaved\n" + require("fs").readFileSync(u.path, "utf8") }
    : { isDirty: false, getText: () => "one\ntwo\nthree\n" },
  registerTextDocumentContentProvider: (s, p) => { exports.providers[s] = p; return { dispose() {} }; } };
exports.window = { showTextDocument: async u => { exports.shown.push({ open: u.path }); } };
exports.commands = { executeCommand: async (c, left, right, title) => {
  if (c === "vscode.diff") exports.shown.push({ diff: right.path, before: exports.providers[left.scheme].provideTextDocumentContent(left), title });
} };`), 0o600)
	os.WriteFile(filepath.Join(ext, "t.cjs"), []byte(`const assert = require("node:assert/strict");
const vscode = require("vscode"), ext = require("./extension.js");
const state = new Map([["acpost.tidied", true]]);
const ctx = () => ({ extensionPath: __dirname, subscriptions: [], workspaceState: { get: k => state.get(k), update: async (k, v) => { state.set(k, v); } } });
const until = async n => { for (let i = 0; i < 100 && vscode.shown.length < n; i++) await new Promise(r => setTimeout(r, 50)); };
(async () => {
  // Requests written before the window was up are shown when it comes up, in the order written
  // (two of one millisecond too); the left side is the editor's text with the edit taken back.
  const before = "one\ntwo\nthree\n";
  const first = ctx();
  await ext.activate(first);
  await until(2);
  assert.deepEqual(vscode.shown, ["early1", "early2"].map(n => ({ diff: process.env.WORK + "/a.txt", before, title: "a.txt (" + n + ")" })));
  console.log("ready");
  await until(3);
  await new Promise(r => setTimeout(r, 300)); // the other folder's request would have come by now
  assert.deepEqual(vscode.shown[2], { diff: process.env.WORK + "/dirty.txt", before: "unsaved\n" + before, title: "dirty.txt (Claude)" });
  assert.equal(vscode.shown.length, 3);
  for (const s of first.subscriptions) s.dispose();
  // The window opened again (the page reloaded): what it showed is not shown a second time.
  const again = ctx();
  await ext.activate(again);
  await new Promise(r => setTimeout(r, 300));
  assert.equal(vscode.shown.length, 3);
  for (const s of again.subscriptions) s.dispose();
  console.log("ok");
})().catch((e) => { console.error(e); process.exit(1); });
`), 0o600)
	dir := filepath.Join(root, "commands")
	at := time.Now()
	for _, title := range []string{"early1", "early2"} { // the same millisecond
		if err := ideReveal(dir, ideRequest{Folder: work, Path: "a.txt", Old: "two", New: "TWO", Title: title}, at); err != nil {
			t.Fatal(err)
		}
	}
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
	if err := ideReveal(dir, ideRequest{Folder: "/elsewhere", Path: filepath.Join(work, "a.txt"), Old: "two", New: "TWO"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	if err := ideReveal(dir, ideRequest{Folder: work, Path: "dirty.txt", Old: "two", New: "TWO", Title: "Claude"}, time.Now()); err != nil {
		t.Fatal(err)
	}
	rest, _ := io.ReadAll(stdout)
	if err := cmd.Wait(); err != nil || !strings.Contains(string(rest), "ok") {
		t.Fatalf("%v: %s%s", err, rest, stderr.String())
	}
}

// A full queue (nothing reads it) lets go of its oldest requests, not the ones just written: two
// written one after the other are both there for the window to read.
func TestIDERevealKeepsTheNewest(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "commands")
	now := time.Now()
	for i := range ideRevealMax + 20 {
		if err := ideReveal(dir, ideRequest{Folder: "/w", Path: fmt.Sprintf("f%03d", i)}, now.Add(time.Duration(i)*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
	}
	os.WriteFile(filepath.Join(dir, ".req-being-written"), []byte("{"), 0o600)
	last := ideRevealMax + 20
	for _, name := range []string{"A", "B"} {
		if err := ideReveal(dir, ideRequest{Folder: "/w", Path: name}, now.Add(time.Duration(last)*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		last++
	}
	entries, _ := os.ReadDir(dir)
	paths := map[string]bool{}
	requests := 0
	for _, e := range entries {
		if !ideRequestName.MatchString(e.Name()) {
			continue
		}
		requests++
		b, _ := os.ReadFile(filepath.Join(dir, e.Name()))
		var r ideRequest
		json.Unmarshal(b, &r)
		paths[r.Path] = true
	}
	if !paths["A"] || !paths["B"] || paths["f000"] || requests > ideRevealMax {
		t.Errorf("%d requests kept; A %v, B %v, the oldest %v", requests, paths["A"], paths["B"], paths["f000"])
	}
	if _, err := os.Stat(filepath.Join(dir, ".req-being-written")); err != nil {
		t.Error("a request being written was removed")
	}
}

// Only the app that owns the IDE asks it to show anything: a second acpost beside it writes no
// request into the folder they share.
func TestIDERevealIsTheOwners(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "commands")
	first, second := &ideServer{}, &ideServer{}
	if owned, _ := first.tryOwn(data); !owned {
		t.Fatal("the first app did not take the IDE")
	}
	defer first.disown()
	if owned, _ := second.tryOwn(data); owned {
		t.Fatal("the second app took the IDE too")
	}
	r := ideRequest{Folder: "/w", Path: "a.txt"}
	if err := second.reveal(dir, r, time.Now()); err == nil {
		t.Error("the second app asked the first's IDE")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d requests written by an app that does not own the IDE", len(entries))
	}
	if err := first.reveal(dir, r, time.Now()); err != nil {
		t.Error(err)
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 1 {
		t.Errorf("%d requests of the owner's", len(entries))
	}
}

// The app on another node (ACPOST_NODE): its agents are named as this node's are, but their
// files are that machine's, and nothing of them is shown in this Mac's IDE.
func TestIDERevealNotForAnotherNode(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, "commands")
	s := &ideServer{}
	if owned, _ := s.tryOwn(data); !owned {
		t.Fatal("the IDE was not taken")
	}
	defer s.disown()
	t.Setenv("ACPOST_NODE", "https://node.example:8443")
	if err := s.reveal(dir, ideRequest{Folder: "/w", Path: "a.txt"}, time.Now()); err == nil {
		t.Error("an edit on another node was asked of this Mac's IDE")
	}
	if entries, _ := os.ReadDir(dir); len(entries) != 0 {
		t.Errorf("%d requests written", len(entries))
	}
	t.Setenv("ACPOST_NODE", "")
	if err := s.reveal(dir, ideRequest{Folder: "/w", Path: "a.txt"}, time.Now()); err != nil {
		t.Error(err)
	}
}
