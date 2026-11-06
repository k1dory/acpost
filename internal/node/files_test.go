package node

import (
	"encoding/base64"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

func file(name, data string) store.File { return store.File{Name: name, Data: []byte(data)} }

func TestSendFilesChecked(t *testing.T) {
	n := newNode(t, `
limits: {max_files: 2, max_files_size: 10}
agents:
  bot: {driver: inbox}
`)
	cases := []struct {
		files []store.File
		code  string
	}{
		{[]store.File{file("a", "1"), file("b", "2"), file("c", "3")}, "refused"},
		{[]store.File{file("a", "12345678901")}, "refused"},
		{[]store.File{file("../x", "1")}, "invalid"},
		{[]store.File{file("", "1")}, "invalid"},
		{[]store.File{file("a\nb", "1")}, "invalid"},
		{[]store.File{file("a", "1"), file("a", "2")}, "conflict"},
	}
	for _, c := range cases {
		_, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "x", Files: c.files})
		if code(err) != c.code {
			t.Errorf("%v: %v, want %s", c.files, err, c.code)
		}
	}
	// Files alone make a message; their types are filled in.
	m, _, err := n.Send(ctx, me, SendRequest{To: "bot", Files: []store.File{file("notes.md", "# a"), file("x.json", "{}")}})
	if err != nil {
		t.Fatal(err)
	}
	if m.Files[0].Type != "text/markdown; charset=utf-8" || m.Files[1].Type != "application/json" {
		t.Fatalf("types %+v", m.Files)
	}
}

func TestInboxFilesRoundTrip(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	m, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "review", Files: []store.File{file("diff.patch", "+x")}})
	if err != nil {
		t.Fatal(err)
	}
	c, ok, err := n.Claim(ctx, me, "bot", time.Second, time.Minute)
	if err != nil || !ok || len(c.Files) != 1 || c.Files[0].Name != "diff.patch" {
		t.Fatalf("claim %+v %v", c.Files, err)
	}
	if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "looks fine", "", "", []store.File{file("review.md", "ok"), file("review.md", "dup")}); code(err) != "conflict" {
		t.Fatalf("duplicate reply files: %v", err)
	}
	if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "looks fine", "", "", []store.File{file("review.md", "ok")}); err != nil {
		t.Fatal(err)
	}
	all, err := n.Files(ctx, me, m.ID)
	if err != nil || len(all) != 2 || all[1].Name != "review.md" || !all[1].Reply || string(all[1].Data) != "ok" {
		t.Fatalf("%+v %v", all, err)
	}
	// Someone who may not see the message does not get its files.
	stranger := Principal{Kind: "client", Name: "x", Token: &config.Token{Name: "x"}}
	if _, err := n.Files(ctx, stranger, m.ID); code(err) != "not_found" {
		t.Fatalf("stranger: %v", err)
	}
	if _, err := n.AttachTurn(me); code(err) != "invalid" || !strings.Contains(hintOf(err), "reply") {
		t.Fatalf("attach outside a turn: %v (%s)", err, hintOf(err))
	}
}

// A tiny PNG.
var png1 = must64("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg==")

func must64(s string) []byte {
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		panic(err)
	}
	return b
}

func TestACPGetsFilesAsBlocks(t *testing.T) {
	n := acpNode(t, "preamble: false")
	files := []store.File{file("notes.md", "# notes"), {Name: "shot.png", Data: png1}, {Name: "data.bin", Data: []byte{0, 1, 2, 255}}}
	w := ask(t, n, SendRequest{Body: "blocks", Files: files})
	if w.State != store.Done {
		t.Fatalf("%s %s", w.State, w.Error)
	}
	lines := strings.Split(strings.TrimSpace(w.Reply), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "resource acpost://") || !strings.HasSuffix(lines[0], `notes.md "# notes"`) ||
		lines[1] != "image image/png "+strconv.Itoa(base64.StdEncoding.EncodedLen(len(png1))) ||
		!strings.HasPrefix(lines[2], "link data.bin \"\\x00\\x01\\x02\\xff\" <nil>") {
		t.Fatalf("blocks:\n%s", w.Reply)
	}
	// Embedded files get no copy on disk; the one sent as a link had one, gone with the turn.
	entries, _ := os.ReadDir(n.turns)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "msg_") {
			t.Fatalf("turn copies left: %v", e.Name())
		}
	}
}

func TestACPPlainAgentGetsLinksAndPreambleLists(t *testing.T) {
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    env: {ACPOST_FAKE_AGENT: "1", FAKE_PLAIN: "1"}
`, os.Args[0]))
	w := ask(t, n, SendRequest{Body: "blocks", Files: []store.File{file("notes.md", "# notes"), {Name: "shot.png", Data: png1}}})
	lines := strings.Split(strings.TrimSpace(w.Reply), "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[0], `link notes.md "# notes" <nil>`) || !strings.HasPrefix(lines[1], "link shot.png") {
		t.Fatalf("blocks:\n%s", w.Reply)
	}
	w = ask(t, n, SendRequest{Body: "prompt", Files: []store.File{file("notes.md", "# notes")}})
	if !strings.Contains(w.Reply, "- notes.md (text/markdown; charset=utf-8, 7 bytes): /") || !strings.Contains(w.Reply, "acpost attach FILE") {
		t.Fatalf("prompt:\n%s", w.Reply)
	}
}

func TestACPFilesCaseAndBudget(t *testing.T) {
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    preamble: false
    env: {ACPOST_FAKE_AGENT: "1"}
`, os.Args[0]))
	old := embedBudget
	embedBudget = 2000
	t.Cleanup(func() { embedBudget = old })
	// Names that differ in case stay two files; past the budget text goes as a link; an empty file is a file.
	w := ask(t, n, SendRequest{Body: "blocks", Files: []store.File{file("A.txt", "upper"), file("a.txt", "lower"), file("big.txt", strings.Repeat("b", 3000)), {Name: "empty.txt"}}})
	got := strings.TrimSpace(w.Reply)
	for _, want := range []string{`A.txt "upper"`, `a.txt "lower"`, `link big.txt "bbb`, `empty.txt`} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %s in:\n%s", want, got)
		}
	}
}

func TestTurnsDirReclaimedAtStart(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "s")
	os.Mkdir(dir, 0o700)
	stale := filepath.Join(dir, "store.db.turns", "msg_old-1")
	os.MkdirAll(stale, 0o700)
	os.WriteFile(filepath.Join(stale, "secret.txt"), []byte("left by a crash"), 0o600)
	os.WriteFile(filepath.Join(dir, "store.db.turns", turnsMark), nil, 0o600) // a node made it
	// What is not this store's stays: another store's copies, anything named turns.
	other := filepath.Join(dir, "turns", "keep.txt")
	os.MkdirAll(filepath.Dir(other), 0o700)
	os.WriteFile(other, []byte("not ours"), 0o600)
	fileNode(t, "store: sqlite://"+dir+"/store.db\nagents:\n  bot: {driver: inbox}\n")
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale turn copy still there: %v", err)
	}
	if _, err := os.Stat(other); err != nil {
		t.Fatalf("removed what was not the store's: %v", err)
	}
}

func TestAttachOnlyInTurnAndWithinLimits(t *testing.T) {
	n := newNode(t, "limits: {max_files: 1}\nagents:\n  bot: {driver: inbox}\n")
	m, _, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"})
	c, _, _ := n.Claim(ctx, me, "bot", time.Second, time.Minute)
	tok := n.issueAgentToken("bot")
	p, _ := n.PrincipalForToken(tok)
	n.setCurrent(tok, m.ID, c.LeaseID)
	attach := func(files ...store.File) error {
		turn, err := n.AttachTurn(p)
		if err == nil {
			_, err = n.Attach(ctx, turn, files)
		}
		return err
	}
	if err := attach(file("one", "1")); err != nil {
		t.Fatal(err)
	}
	if err := attach(file("two", "2")); code(err) != "refused" {
		t.Fatalf("second file over max_files 1: %v", err)
	}
	// An upload begun in one attempt goes to that attempt or nowhere, even if another runs by then.
	turn, _ := n.AttachTurn(p)
	n.setCurrent(tok, m.ID, "the-next-attempt")
	if _, err := n.Attach(ctx, turn, []store.File{file("x", "1")}); code(err) != "refused" {
		t.Fatalf("held turn still counts limits: %v", err)
	}
	if err := attach(file("y", "1")); code(err) != "conflict" {
		t.Fatalf("stale lease: %v", err)
	}
}

// A body that grows six times as JSON leaves less room to embed: the file goes as a link, and the
// request stays inside the agent's frame.
func TestACPPromptBudgetCountsTheBody(t *testing.T) {
	n := newNode(t, fmt.Sprintf(`
limits: {max_body: 2MiB, max_files_size: 8MiB}
agents:
  fake:
    driver: acp
    command: [%q]
    preamble: false
    env: {ACPOST_FAKE_AGENT: "1"}
`, os.Args[0]))
	body := strings.Repeat("\x00", 1<<20) + "\nblocks"
	big := store.File{Name: "big.txt", Data: []byte(strings.Repeat("a", 3<<20))}
	w := ask(t, n, SendRequest{Body: body, Files: []store.File{big}})
	if w.State != store.Done || !strings.HasPrefix(strings.TrimSpace(w.Reply), "link big.txt") {
		t.Fatalf("%s %s %.200q", w.State, w.Error, w.Reply)
	}
}

func TestTurnsDirOwnershipAndAbsolute(t *testing.T) {
	// A path of the turns' name that acpost did not make stops the start, and is left alone.
	dir := filepath.Join(t.TempDir(), "s")
	os.Mkdir(dir, 0o700)
	os.WriteFile(filepath.Join(dir, "store.db.turns"), []byte("another store"), 0o600)
	cfg, _ := config.Parse([]byte("store: sqlite://"+dir+"/store.db\nagents:\n  bot: {driver: inbox}\n"), ".yaml")
	st, err := store.Open(cfg.Store, StoreOptions(cfg))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	n, _ := New(cfg, st, nil)
	if err := n.Start(ctx); err == nil || !strings.Contains(err.Error(), "move it away") {
		t.Fatalf("start over a foreign path: %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "store.db.turns")); string(b) != "another store" {
		t.Fatal("the foreign file was touched")
	}

	// A relative store path still gives agents absolute links.
	wd, _ := os.Getwd()
	base := t.TempDir()
	os.Chdir(base)
	defer os.Chdir(wd)
	os.Mkdir("rel", 0o700)
	n2, _ := fileNode(t, "store: sqlite://rel/store.db\nagents:\n  bot: {driver: inbox}\n")
	if !filepath.IsAbs(n2.turns) {
		t.Fatalf("turns %q", n2.turns)
	}
}

func TestLongGivenTypeIgnored(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	m, _, err := n.Send(ctx, me, SendRequest{To: "bot", Files: []store.File{{Name: "x.txt", Type: "text/plain; x=" + strings.Repeat("a", 1000), Data: []byte("x")}}})
	if err != nil || m.Files[0].Type != "text/plain; charset=utf-8" {
		t.Fatalf("%v %q", err, m.Files[0].Type)
	}
}

// A body that alone would overflow the agent's frame reaches it as a file the prompt names.
func TestACPHugeBodyGoesAsFile(t *testing.T) {
	n := newNode(t, fmt.Sprintf(`
limits: {max_body: 2MiB}
agents:
  fake:
    driver: acp
    command: [%q]
    preamble: false
    env: {ACPOST_FAKE_AGENT: "1"}
`, os.Args[0]))
	w := ask(t, n, SendRequest{Body: strings.Repeat("\x00", 2<<20-16) + "\nprompt"})
	// The fake agent answers about the prompt's last line: the pointer to the file, not the body.
	if w.State != store.Done || w.Reply != "unknown: [acpost]" {
		t.Fatalf("%s %s %.300q", w.State, w.Error, w.Reply)
	}
}

func TestLabelsBounded(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	long := strings.Repeat("t", maxLabel+1)
	for _, r := range []SendRequest{{Task: long}, {Thread: long}, {IdemKey: long}} {
		r.To, r.Body = "bot", "x"
		if _, _, err := n.Send(ctx, me, r); code(err) != "invalid" {
			t.Fatalf("%v", err)
		}
	}
}

// Files in the agent's answer are kept with its reply, each the cheapest way that loses nothing.
func TestACPReplyFilesCollected(t *testing.T) {
	disk := filepath.Join(t.TempDir(), "made.md")
	os.WriteFile(disk, []byte("on disk"), 0o600)
	n := newNode(t, fmt.Sprintf(`
agents:
  fake:
    driver: acp
    command: [%q]
    preamble: false
    env: {ACPOST_FAKE_AGENT: "1", FAKE_DISK: %q}
`, os.Args[0], disk))
	w := ask(t, n, SendRequest{Body: "give", Files: []store.File{file("notes.md", "# notes")}})
	if w.State != store.Done || w.Reply != "here" {
		t.Fatalf("%s %q %s: the files are not in the text", w.State, w.Reply, w.Error)
	}
	got := map[string]store.File{}
	var given store.File
	for _, f := range w.Files {
		if f.Reply {
			got[f.Name] = f
		} else {
			given = f
		}
	}
	check := func(name string, ok bool) {
		t.Helper()
		if !ok {
			t.Fatalf("%s: %+v\nall: %+v", name, got[name], w.Files)
		}
	}
	img := got["image-1.png"]
	check("image-1.png", !img.Link() && img.Size == len(png1) && img.Type == "image/png")
	check("spec.pdf", got["spec.pdf"].URI == "https://example.com/download?id=42") // the name it gave, not the URI's
	check("made.md", got["made.md"].URI == "file://"+disk && got["made.md"].Size == 7)
	check("new.txt", !got["new.txt"].Link() && got["new.txt"].Size == 5)
	// The file it was given, handed back: the stored file again, the same content, not a new one.
	back := got["notes.md"]
	check("notes.md", !back.Link() && back.SHA256 == given.SHA256 && back.SHA256 != "")
	all, _ := n.Files(ctx, me, w.ID)
	for _, f := range all {
		if f.Name == "made.md" && f.Data != nil {
			t.Fatal("a file on disk was copied into the store")
		}
	}
}

// A file handed back changed is the changed file, not the one it was given.
func TestACPEditedFileKeptAsEdited(t *testing.T) {
	n := newNode(t, fmt.Sprintf("agents:\n  fake:\n    driver: acp\n    command: [%q]\n    preamble: false\n    env: {ACPOST_FAKE_AGENT: \"1\"}\n", os.Args[0]))
	w := ask(t, n, SendRequest{Body: "edit", Files: []store.File{file("report.md", "draft")}})
	all, _ := n.Files(ctx, me, w.ID)
	var back store.File
	for _, f := range all {
		if f.Reply {
			back = f
		}
	}
	if back.Name != "report.md" || string(back.Data) != "draft (fixed)" {
		t.Fatalf("%+v", back)
	}
}

// Past the reply's limits the files that fit stay, in order; the rest is said.
func TestACPReplyFilesPartlyKept(t *testing.T) {
	n := newNode(t, fmt.Sprintf("limits: {max_files: 2}\nagents:\n  fake:\n    driver: acp\n    command: [%q]\n    preamble: false\n    env: {ACPOST_FAKE_AGENT: \"1\"}\n", os.Args[0]))
	w := ask(t, n, SendRequest{Body: "give"})
	var names []string
	for _, f := range w.Files {
		names = append(names, f.Name)
	}
	if strings.Join(names, " ") != "image-1.png spec.pdf" {
		t.Fatalf("kept %v", names)
	}
	evs, _ := n.Events(ctx, me, w.ID, 0, 100)
	said := false
	for _, e := range evs {
		said = said || (e.Kind == "notice" && strings.Contains(e.Data, "more files than the reply keeps"))
	}
	if !said {
		t.Fatalf("not said: %+v", evs)
	}
}

func TestOnDiskRefusesFIFOAtOnce(t *testing.T) {
	fifo := filepath.Join(t.TempDir(), "p")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Skip(err)
	}
	done := make(chan bool, 1)
	go func() { done <- onDisk("file://"+fifo, []byte("x")) }()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("a FIFO taken for a file on disk")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("onDisk waits on a FIFO")
	}
}

// A file at the size limit is kept, and it has its type.
func TestACPReplyFileAtLimitTyped(t *testing.T) {
	n := newNode(t, fmt.Sprintf("limits: {max_files_size: 5}\nagents:\n  fake:\n    driver: acp\n    command: [%q]\n    preamble: false\n    env: {ACPOST_FAKE_AGENT: \"1\"}\n", os.Args[0]))
	w := ask(t, n, SendRequest{Body: "give"})
	for _, f := range w.Files {
		if f.Name == "new.txt" {
			if f.Type != "text/plain; charset=utf-8" || f.Size != 5 {
				t.Fatalf("%+v", f)
			}
			return
		}
	}
	t.Fatalf("new.txt (5 bytes, the limit) not kept: %+v", w.Files)
}

func TestJoinWithoutTokenSaysWhich(t *testing.T) {
	t.Setenv("ACPOST_TEST_UNSET_TOKEN", "")
	cfg, err := config.Parse([]byte("store: memory\nnode: {join: {url: https://hub:7420, token_env: ACPOST_TEST_UNSET_TOKEN}}\n"), ".yaml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := New(cfg, nil, slog.Default()); err == nil || !strings.Contains(err.Error(), "ACPOST_TEST_UNSET_TOKEN is not set") {
		t.Fatalf("%v", err)
	}
}

// The guard reads the scheme as url.Parse does: FILE:// is a local link too.
func TestLocalLinkAnyCase(t *testing.T) {
	for _, uri := range []string{"file:///tmp/x", "FILE:///tmp/x", "File:///tmp/x"} {
		if err := noLocalLinks([]store.File{{Name: "x", URI: uri}}, "agent:b@n", sendCopyHint); err == nil {
			t.Fatalf("%s crossed", uri)
		}
	}
	if err := noLocalLinks([]store.File{{Name: "x", URI: "https://example.com/file:x"}}, "agent:b@n", sendCopyHint); err != nil {
		t.Fatal(err)
	}
}

// A joined node answering on its hub lease is another machine: its file:// links are refused, even
// for a message the hub's own user sent.
func TestHubRefusesNodeReplyLinks(t *testing.T) {
	hub := newNode(t, "node: {name: hub}\n")
	edge := Principal{Kind: "node", Name: "edge"}
	if err := hub.Announce(ctx, edge, "edge", []string{"bot"}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := hub.Send(ctx, Principal{Kind: "user", Name: "boss"}, SendRequest{To: "agent:bot@edge", Body: "run"}); err != nil {
		t.Fatal(err)
	}
	m, ok, err := hub.NodeClaim(ctx, edge, "edge", 0, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	link := []store.File{{Name: "r.txt", URI: "file:///tmp/r.txt"}}
	if err := hub.ReplyCoded(ctx, edge, m.ID, m.LeaseID, "done", "", "", link); code(err) != "invalid" {
		t.Fatalf("hub took a link from a node: %v", err)
	}
	if err := hub.ReplyCoded(ctx, edge, m.ID, m.LeaseID, "done", "", "", []store.File{{Name: "r.txt", Data: []byte("r")}}); err != nil {
		t.Fatal(err)
	}
}
