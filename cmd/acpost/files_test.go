package main

import (
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

func TestAttachFilesBothWays(t *testing.T) {
	e := newEnv(t, "agents:\n"+fakeAgent("a", "")+"  bot: {driver: inbox}\n")
	e.serve()
	in := filepath.Join(e.dir, "report.md")
	os.WriteFile(in, []byte("# Report\nall good\n"), 0o600)

	// To an ACP agent: the text is embedded in its prompt.
	if out := e.ok("", "send", "--to", "a", "--attach", in, "--body", "blocks", "--wait", "--timeout", "20s"); !strings.Contains(out, `report.md "# Report\nall good\n"`) {
		t.Fatalf("agent got %q", out)
	}

	// The agent attaches a file to its reply from inside its turn.
	r := e.run("", "send", "--to", "a", "--body", "shell printf 'made by a' > out.txt && acpost attach out.txt", "--wait", "--timeout", "20s")
	if r.code != 0 || !strings.Contains(r.out, "attached 1 file(s) to the reply") || !strings.Contains(r.err, "attached to the reply: out.txt (text/plain; charset=utf-8, 9 bytes)") {
		t.Fatalf("exit %d\n%s\n%s", r.code, r.out, r.err)
	}
	id := strings.Fields(r.err[strings.Index(r.err, "acpost files ")+len("acpost files "):])[0]
	id = strings.TrimSuffix(id, "`")
	dir := filepath.Join(e.dir, "got")
	if r := e.run("", "files", id); r.code != 3 || !strings.Contains(r.err, "--reply") {
		t.Fatalf("message side: %d %s", r.code, r.err)
	}
	if out := e.ok("", "files", id, "--reply", "-o", dir); strings.TrimSpace(out) != filepath.Join(dir, "out.txt") {
		t.Fatalf("files printed %q", out)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "out.txt")); string(b) != "made by a" {
		t.Fatalf("saved %q", b)
	}
	if r := e.run("", "files", id, "--reply", "-o", dir); r.code != 1 || !strings.Contains(r.err, "--force") {
		t.Fatalf("overwrite: %d %s", r.code, r.err)
	}
	if out := e.ok("", "files", id, "out.txt", "--reply", "--stdout"); out != "made by a" {
		t.Fatalf("stdout %q", out)
	}
	if r := e.run("", "files", id, "nope", "--reply"); r.code != 3 {
		t.Fatalf("missing file: %d %s", r.code, r.err)
	}

	// An inbox: recv lists the files, reply attaches.
	sent := strings.TrimSpace(e.ok("", "send", "--to", "bot", "--attach", in))
	got := e.ok("", "recv", "--as", "bot")
	if !strings.Contains(got, "file: report.md (text/markdown; charset=utf-8, 18 bytes)") {
		t.Fatalf("recv %q", got)
	}
	lease := strings.TrimPrefix(strings.Split(got, "\n")[1], "lease: ")
	e.ok("", "reply", sent, "--lease", lease, "--body", "see file", "--attach", in)
	show := e.ok("", "show", sent)
	if !strings.Contains(show, "reply file:  report.md") || !strings.Contains(show, "file:        report.md") {
		t.Fatalf("show:\n%s", show)
	}

	// send --wait --save: the reply's files land in a directory, one call.
	saveDir := filepath.Join(e.dir, "saved")
	r = e.run("", "send", "--to", "a", "--body", "shell printf two > two.txt && acpost attach two.txt", "--wait", "--timeout", "20s", "--save", saveDir)
	if r.code != 0 || !strings.Contains(r.err, "saved: "+filepath.Join(saveDir, "two.txt")) {
		t.Fatalf("--save: %d\n%s\n%s", r.code, r.out, r.err)
	}
	if b, _ := os.ReadFile(filepath.Join(saveDir, "two.txt")); string(b) != "two" {
		t.Fatalf("--save wrote %q", b)
	}
	saveID := strings.Fields(strings.Split(e.ok("", "list", "--to", "a"), "\n")[2])[0] // the third message to a
	if r := e.run("", "wait", saveID, "--save", saveDir); r.code != 1 || !strings.Contains(r.err, "--save names another directory") {
		t.Fatalf("save again: %d %s", r.code, r.err)
	}
	if out := e.ok("", "wait", saveID, "--save", filepath.Join(e.dir, "saved2"), "--json"); !strings.Contains(out, `"saved":[{"name":"two.txt"`) {
		t.Fatalf("--json --save: %s", out)
	}

	// Two paths with one base name are caught before anything is sent.
	os.MkdirAll(filepath.Join(e.dir, "x"), 0o700)
	os.WriteFile(filepath.Join(e.dir, "x", "report.md"), []byte("other"), 0o600)
	if r := e.run("", "send", "--to", "bot", "--attach", in, "--attach", filepath.Join(e.dir, "x", "report.md")); r.code != 2 || !strings.Contains(r.err, "would both go as report.md") {
		t.Fatalf("duplicate base names: %d %s", r.code, r.err)
	}
	if r := e.run("", "send", "--to", "bot", "--attach", e.dir); r.code != 2 || !strings.Contains(r.err, "one by one") {
		t.Fatalf("a directory: %d %s", r.code, r.err)
	}

	// The wrong side says where the files are.
	if r := e.run("", "files", id); !strings.Contains(r.err, "its reply has 1: `acpost files "+id+" --reply`") {
		t.Fatalf("wrong side hint: %s", r.err)
	}

	// Outside a turn, attach says what to do instead.
	if r := e.run("", "attach", in); r.code != 2 || !strings.Contains(r.err, "reply ID --lease L --attach") || !strings.Contains(r.err, "ACPOST_TOKEN") {
		t.Fatalf("attach outside: %d %s", r.code, r.err)
	}
}

func TestAttachAcrossHub(t *testing.T) {
	a, b := hubPair(t)
	in := filepath.Join(b.dir, "notes.md")
	os.WriteFile(in, []byte("from b"), 0o600)
	deadline := time.Now().Add(15 * time.Second)
	for {
		r := b.run("", "send", "--to", "agent:reviewer", "--attach", in, "--body", "shell printf back > r.txt && acpost attach r.txt && echo ok", "--wait", "--timeout", "20s")
		if r.code == 0 {
			if !strings.Contains(r.err, "attached to the reply: r.txt") {
				t.Fatalf("%q %q", r.out, r.err)
			}
			id := strings.TrimSuffix(strings.Fields(r.err[strings.Index(r.err, "acpost files ")+len("acpost files "):])[0], "`")
			if out := b.ok("", "files", id, "r.txt", "--reply", "--stdout"); out != "back" {
				t.Fatalf("reply file via hub %q", out)
			}
			// The serving node got the message's file.
			if out := a.ok("", "files", id, "notes.md", "--stdout"); out != "from b" {
				t.Fatalf("message file on a %q", out)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("never routed: %d %s", r.code, r.err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// A node that fails to start says why, not only where its log is.
func TestAutostartFailureSaysWhy(t *testing.T) {
	e := newEnv(t, inbox)
	os.MkdirAll(filepath.Join(e.dir, "s"), 0o755)
	os.Chmod(filepath.Join(e.dir, "s"), 0o755)
	r := e.run("", "status")
	if r.code != 6 || !strings.Contains(r.err, "did not start: ") || !strings.Contains(r.err, "open to other users") {
		t.Fatalf("%d %s", r.code, r.err)
	}
}

// Reply files a node on the way does not take: the text arrives, the failure says why (files_refused).
func TestReplyFilesRefusedOnTheWay(t *testing.T) {
	for _, c := range []struct{ name, b, hub string }{
		{"sender", "limits: {max_files_size: 8}\n", ""},
		{"hub", "", "limits: {max_files_size: 8}\n"},
	} {
		t.Run(c.name, func(t *testing.T) {
			_, b := hubPair(t, c.b, c.hub)
			deadline := time.Now().Add(15 * time.Second)
			for {
				r := b.run("", "send", "--to", "agent:reviewer", "--body", "shell printf 'well over eight bytes' > big.txt && acpost attach big.txt && echo text-kept", "--wait", "--timeout", "20s", "--json")
				if r.code == 5 {
					if !strings.Contains(r.out, `"error_code":"files_refused"`) || !strings.Contains(r.out, "text-kept") || !strings.Contains(r.err, "max_files_size") {
						t.Fatalf("out %s\nerr %s", r.out, r.err)
					}
					return
				}
				if time.Now().After(deadline) || (r.code != 3 && r.code != 6) {
					t.Fatalf("exit %d\n%s\n%s", r.code, r.out, r.err)
				}
				time.Sleep(200 * time.Millisecond)
			}
		})
	}
}

func TestSaveFilesStaysInDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"../out.txt", "a/b", "..", ""} {
		if _, err := saveFiles([]store.File{{Name: name, Data: []byte("x")}}, dir, true, ""); err == nil {
			t.Fatalf("saved %q", name)
		}
	}
	// --force replaces a symlink; its target is not written.
	target := filepath.Join(t.TempDir(), "target")
	os.WriteFile(target, []byte("keep"), 0o600)
	os.Symlink(target, filepath.Join(dir, "f.txt"))
	if _, err := saveFiles([]store.File{{Name: "f.txt", Data: []byte("new")}}, dir, false, "h"); err == nil {
		t.Fatal("overwrote without --force")
	}
	if _, err := saveFiles([]store.File{{Name: "f.txt", Data: []byte("new")}}, dir, true, ""); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "keep" {
		t.Fatalf("symlink target written: %q", b)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "f.txt")); string(b) != "new" {
		t.Fatalf("saved %q", b)
	}
}

func TestAttachChecksLimitsFirst(t *testing.T) {
	e := newEnv(t, "limits: {max_files_size: 1KiB}\n"+inbox)
	e.serve()
	big := filepath.Join(e.dir, "big.bin")
	os.WriteFile(big, make([]byte, 4096), 0o600)
	if r := e.run("", "send", "--to", "bot", "--attach", big); r.code != 7 || !strings.Contains(r.err, "max_files_size (1024)") {
		t.Fatalf("%d %s", r.code, r.err)
	}
	// --save without --wait is a usage error before anything is sent.
	if r := e.run("", "send", "--to", "bot", "--body", "x", "--save", e.dir); r.code != 2 {
		t.Fatalf("--save alone: %d %s", r.code, r.err)
	}
	if out := e.ok("", "list", "--to", "bot"); strings.TrimSpace(out) != "" {
		t.Fatalf("something was sent:\n%s", out)
	}
}

func TestForceKeepsModeAndUnreachableNodeReadsNothing(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "p.txt"), []byte("old"), 0o600)
	if _, err := saveFiles([]store.File{{Name: "p.txt", Data: []byte("new")}}, dir, true, ""); err != nil {
		t.Fatal(err)
	}
	if st, _ := os.Stat(filepath.Join(dir, "p.txt")); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %o after --force", st.Mode().Perm())
	}
	e := newEnv(t, inbox)
	in := filepath.Join(e.dir, "f.txt")
	os.WriteFile(in, []byte("x"), 0o600)
	if r := e.run("", "send", "--node", "unix:///tmp/acpost-nowhere.sock", "--to", "bot", "--attach", in); r.code != 6 {
		t.Fatalf("unreachable node: %d %s", r.code, r.err)
	}
}

func TestAttachFIFORefusedAtOnce(t *testing.T) {
	e := newEnv(t, inbox)
	e.serve()
	fifo := filepath.Join(e.dir, "input")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	done := make(chan result, 1)
	go func() { done <- e.run("", "send", "--to", "bot", "--body", "x", "--attach", fifo) }()
	select {
	case r := <-done:
		if r.code != 2 || !strings.Contains(r.err, "is not a file") {
			t.Fatalf("%d %s", r.code, r.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("attach of a FIFO hangs")
	}
}

// A node that would not take a delivered message's files refuses the message; it does not run it without them.
func TestDeliveredFilesOverLimitRefused(t *testing.T) {
	_, b := hubPair(t, "", "", "limits: {max_files_size: 8}\n")
	in := filepath.Join(b.dir, "big.txt")
	os.WriteFile(in, []byte("well over eight bytes"), 0o600)
	deadline := time.Now().Add(15 * time.Second)
	for {
		r := b.run("", "send", "--to", "agent:reviewer", "--attach", in, "--body", "echo ran-anyway", "--wait", "--timeout", "20s")
		if r.code == 5 {
			if strings.Contains(r.out, "ran-anyway") || !strings.Contains(r.err, "max_files_size") {
				t.Fatalf("out %q err %q", r.out, r.err)
			}
			return
		}
		if time.Now().After(deadline) || (r.code != 3 && r.code != 6) {
			t.Fatalf("exit %d\n%s\n%s", r.code, r.out, r.err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
