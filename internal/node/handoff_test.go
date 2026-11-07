package node

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// A repository with work not on its remote: a commit not pushed, a file changed, a new file, a
// secret. The patch carries the work from the base on the remote; the secret is named, not sent;
// credentials in the remote's URL are dropped.
func handoffRepo(t *testing.T) string {
	root := t.TempDir()
	bare, dir := filepath.Join(root, "origin.git"), filepath.Join(root, "work")
	gitIn(t, root, "init", "-q", "--bare", "-b", "main", bare)
	gitIn(t, root, "init", "-q", "-b", "main", dir)
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n"), 0o644)
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "one")
	gitIn(t, dir, "remote", "add", "origin", bare)
	gitIn(t, dir, "push", "-q", "-u", "origin", "main")
	// The cloud clones over the network: the remote as it would be (origin/main stays known).
	gitIn(t, dir, "remote", "set-url", "origin", "https://git.example/o/r.git")
	os.WriteFile(filepath.Join(root, "bare"), []byte(bare), 0o644)
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a // committed, not pushed\n"), 0o644)
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "two")
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\n// changed\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package a // new\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=supersecret\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "secrets"), 0o755)
	os.WriteFile(filepath.Join(dir, "secrets", "token.txt"), []byte("dirsecret\n"), 0o644)
	os.MkdirAll(filepath.Join(dir, "app"), 0o755)
	os.WriteFile(filepath.Join(dir, "app", "deep.go"), []byte("package app // deep\n"), 0o644)
	return dir
}

func TestReadRepo(t *testing.T) {
	dir := handoffRepo(t)
	rs, err := readRepo(ctx, filepath.Join(dir, "app")) // from a subdirectory: the whole repository
	if err != nil {
		t.Fatal(err)
	}
	p := string(rs.patch)
	for _, want := range []string{"committed, not pushed", "// changed", "package a // new", "b/app/deep.go"} {
		if !strings.Contains(p, want) {
			t.Fatalf("no %q in the patch:\n%s", want, p)
		}
	}
	if strings.Contains(p, "supersecret") || strings.Contains(p, "dirsecret") || len(rs.left) != 2 {
		t.Fatalf("the secret: left %v\n%s", rs.left, p)
	}
	if rs.base == "" || rs.branch != "main" {
		t.Fatalf("%+v", rs)
	}
	// The patch applies to the base.
	clone := filepath.Join(t.TempDir(), "c")
	bare, _ := os.ReadFile(filepath.Join(filepath.Dir(dir), "bare"))
	gitIn(t, filepath.Dir(clone), "clone", "-q", string(bare), clone)
	gitIn(t, clone, "checkout", "-q", rs.base)
	os.WriteFile(filepath.Join(clone, "h.patch"), rs.patch, 0o644)
	gitIn(t, clone, "apply", "--index", "h.patch")
	for _, f := range []string{"new.go", "app/deep.go"} {
		if b, _ := os.ReadFile(filepath.Join(clone, f)); !strings.Contains(string(b), "package") {
			t.Fatalf("%s did not come", f)
		}
	}
	for in, want := range map[string]string{
		"https://user:pw@git.example/o/r.git?x=1": "https://git.example/o/r.git",
		"ssh://git:pw@git.example/o/r.git":        "ssh://git@git.example/o/r.git",
		"git@github.com:o/r.git":                  "git@github.com:o/r.git",
	} {
		if got, err := plainRemote(in); err != nil || got != want {
			t.Fatalf("%s: %s %v", in, got, err)
		}
	}
	for _, bad := range []string{"https://alice:pa%ss@git.example/repo.git", "ftp://x/y", "not a remote"} {
		if got, err := plainRemote(bad); err == nil {
			t.Fatalf("%s passed as %s", bad, got)
		}
	}
	if handoffBranch("topic;id;") != "handoff/topic-id" || handoffBranch("") != "handoff" {
		t.Fatal(handoffBranch("topic;id;"))
	}
	// No base known on the remote: refused, not guessed.
	lone := t.TempDir()
	gitIn(t, lone, "init", "-q", "-b", "main")
	os.WriteFile(filepath.Join(lone, "x"), []byte("x\n"), 0o644)
	gitIn(t, lone, "add", ".")
	gitIn(t, lone, "commit", "-qm", "x")
	gitIn(t, lone, "remote", "add", "origin", "https://git.example/o/r.git")
	if _, err := readRepo(ctx, lone); code(err) != "invalid" {
		t.Fatalf("no base: %v", err)
	}
	if _, err := readRepo(ctx, t.TempDir()); code(err) != "invalid" {
		t.Fatalf("not a repository: %v", err)
	}
}

// A thread handed off: a sandbox on the repository's remote, its first message with the base,
// the conversation and the patch.
func TestHandoff(t *testing.T) {
	f := &fakeSandboxd{t: t, answered: make(chan struct{}), id: "sb_abc123"}
	f.frames = []string{"USER:6"}
	srv := httptest.NewServer(http.HandlerFunc(f.handler))
	t.Cleanup(srv.Close)
	env := filepath.Join(t.TempDir(), "pilot.env")
	os.WriteFile(env, []byte("export SANDBOXD_URL="+srv.URL+"\nexport SANDBOXD_TOKEN='sbtoken'\n"), 0o600)
	n := newNode(t, inboxCfg+"cloud: {env_file: "+env+"}\n")
	dir := handoffRepo(t)
	first, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "make the parser faster"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.Handoff(ctx, Principal{Kind: "agent", Name: "bot"}, HandoffRequest{Thread: first.Thread, Cwd: dir, CloudAgent: "assistant"}); code(err) != "refused" {
		t.Fatalf("an agent: %v", err)
	}
	res, err := n.Handoff(ctx, me, HandoffRequest{Thread: cmpOr(first.Thread, first.ID), Cwd: dir, CloudAgent: "assistant", Note: "finish it"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Chat.Sandbox != "sb_abc123" || res.Patch == 0 || len(res.Left) != 2 {
		t.Fatalf("%+v", res)
	}
	f.mu.Lock()
	created := f.created[0]
	f.mu.Unlock()
	if created["repo"] != "https://git.example/o/r.git" {
		t.Fatalf("%v", created)
	}
	m, err := n.st.Get(ctx, res.Message)
	if err != nil || !strings.Contains(m.Body, "make the parser faster") || !strings.Contains(m.Body, res.Base) || !strings.Contains(m.Body, "finish it") || !strings.Contains(m.Body, ".env") {
		t.Fatalf("%v\n%s", err, m.Body)
	}
	files, _ := n.st.FilesOf(ctx, m.ID)
	if len(files) != 1 || files[0].Name != "handoff.patch" || strings.Contains(string(files[0].Data), "supersecret") {
		t.Fatalf("%+v", files)
	}
	_ = store.Done
}
