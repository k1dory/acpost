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
	os.WriteFile(filepath.Join(dir, "b.go"), []byte("package a // committed, not pushed\n"), 0o644)
	gitIn(t, dir, "add", ".")
	gitIn(t, dir, "commit", "-qm", "two")
	os.WriteFile(filepath.Join(dir, "a.go"), []byte("package a\n\n// changed\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package a // new\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".env"), []byte("TOKEN=supersecret\n"), 0o644)
	return dir
}

func TestReadRepo(t *testing.T) {
	dir := handoffRepo(t)
	rs, err := readRepo(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	p := string(rs.patch)
	for _, want := range []string{"committed, not pushed", "// changed", "package a // new"} {
		if !strings.Contains(p, want) {
			t.Fatalf("no %q in the patch:\n%s", want, p)
		}
	}
	if strings.Contains(p, "supersecret") || len(rs.left) != 1 || rs.left[0] != ".env" {
		t.Fatalf("the secret: left %v\n%s", rs.left, p)
	}
	if rs.base == "" || rs.branch != "main" || rs.unpushed {
		t.Fatalf("%+v", rs)
	}
	// The patch applies to the base.
	clone := filepath.Join(t.TempDir(), "c")
	gitIn(t, filepath.Dir(clone), "clone", "-q", rs.remote, clone)
	gitIn(t, clone, "checkout", "-q", rs.base)
	os.WriteFile(filepath.Join(clone, "h.patch"), rs.patch, 0o644)
	gitIn(t, clone, "apply", "--index", "h.patch")
	if b, _ := os.ReadFile(filepath.Join(clone, "new.go")); !strings.Contains(string(b), "new") {
		t.Fatal("the new file did not come")
	}
	if plainRemote("https://user:pw@git.example/o/r.git?x=1") != "https://git.example/o/r.git" {
		t.Fatal(plainRemote("https://user:pw@git.example/o/r.git?x=1"))
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
	if res.Chat.Sandbox != "sb_abc123" || res.Patch == 0 || len(res.Left) != 1 {
		t.Fatalf("%+v", res)
	}
	f.mu.Lock()
	created := f.created[0]
	f.mu.Unlock()
	if !strings.HasSuffix(created["repo"].(string), "origin.git") {
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
