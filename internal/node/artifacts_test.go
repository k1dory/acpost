package node

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/plat"
	"github.com/k1dory/acpost/internal/store"
)

func TestArtifactsLookAtFilesAndNameChats(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte("# five bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "linked.html"), []byte("<p>hi</p>"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &store.Message{ID: store.NewID(), From: "user:tester", To: "agent:bot", Body: "make a report"}
	if _, _, err := n.st.Insert(ctx, m); err != nil {
		t.Fatal(err)
	}
	c, _, err := n.st.Claim(ctx, "agent:bot", time.Minute)
	if err != nil || c.ID != m.ID {
		t.Fatal(c.ID, err)
	}
	link := store.File{Name: "linked.html", Type: "text/html", URI: plat.FileURI(filepath.Join(dir, "linked.html"))}
	if err := n.st.AttachReply(ctx, m.ID, c.LeaseID, []store.File{link}, store.FileLimits{Count: 4, Size: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().Add(time.Hour)
	ch := store.TurnChanges{Msg: m.ID, Agent: "agent:bot", Start: at, At: at, Repo: dir,
		Files: []store.ChangedFile{{Path: "report.md", Status: "A"}, {Path: "gone.png", Status: "A"}}}
	if err := n.st.AddChanges(ctx, ch); err != nil {
		t.Fatal(err)
	}
	if err := n.st.SetChatTitle(ctx, "thread:"+m.ID, "Quarterly"); err != nil {
		t.Fatal(err)
	}

	got, err := n.Artifacts(ctx, me, store.ArtifactQuery{})
	if err != nil || len(got.Items) != 3 || got.Next != "" {
		t.Fatalf("%+v %v", got, err)
	}
	byName := map[string]store.Artifact{}
	for _, a := range got.Items {
		byName[a.Name] = a
		if a.Title != "Quarterly" {
			t.Errorf("%s: chat %q, not the name the user gave it", a.Name, a.Title)
		}
	}
	if r := byName["report.md"]; r.Gone || r.Size != 12 || r.Type == "" || r.Path != filepath.Join(dir, "report.md") {
		t.Errorf("report %+v", r)
	}
	if g := byName["gone.png"]; !g.Gone {
		t.Errorf("a file no longer there: %+v", g)
	}
	if l := byName["linked.html"]; l.Kind != "link" || l.Path != filepath.Join(dir, "linked.html") || l.Gone || l.Size != 9 {
		t.Errorf("a link to a file here %+v", l)
	}

	// A page at a time.
	page, err := n.Artifacts(ctx, me, store.ArtifactQuery{Limit: 2})
	if err != nil || len(page.Items) != 2 || page.Next == "" {
		t.Fatalf("%+v %v", page, err)
	}
	if _, err := n.Artifacts(ctx, me, store.ArtifactQuery{Before: "x"}); CodeOf(err) != "invalid" {
		t.Errorf("a bad cursor: %v", err)
	}
}

func TestArtifactsAreTheUsers(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	agent := Principal{Kind: "agent", Name: "bot"}
	if _, err := n.Artifacts(ctx, agent, store.ArtifactQuery{}); CodeOf(err) != "refused" {
		t.Errorf("an agent listed the artifacts: %v", err)
	}
	if _, err := n.ArtifactHead(ctx, agent, "m", "a", 10); CodeOf(err) != "refused" {
		t.Errorf("an agent read an artifact: %v", err)
	}
	if _, err := n.ArtifactHead(ctx, me, "nope", "a", 10); CodeOf(err) != "not_found" {
		t.Errorf("a missing file: %v", err)
	}
	if got, err := n.Artifacts(ctx, me, store.ArtifactQuery{}); err != nil || got.Items == nil {
		t.Errorf("an empty library is an empty list: %+v %v", got, err)
	}
}

func TestArtifactHeadCutsContent(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	m := &store.Message{ID: store.NewID(), From: "user:tester", To: "agent:bot", Body: "draw"}
	if _, _, err := n.st.Insert(ctx, m); err != nil {
		t.Fatal(err)
	}
	c, _, _ := n.st.Claim(ctx, "agent:bot", time.Minute)
	f := store.File{Name: "a.txt", Type: "text/plain", Data: []byte("line one\nline two\n")}
	if err := n.st.AttachReply(ctx, m.ID, c.LeaseID, []store.File{f}, store.FileLimits{Count: 4, Size: 1 << 20}); err != nil {
		t.Fatal(err)
	}
	got, err := n.ArtifactHead(ctx, me, m.ID, "a.txt", 8)
	if err != nil || string(got.Data) != "line one" || got.Size != 18 {
		t.Fatalf("%+v %v", got, err)
	}
	if got, err := n.ArtifactHead(ctx, me, m.ID, "a.txt", 0); err != nil || got.Size != len(got.Data) {
		t.Fatalf("no bound asked: %+v %v", got, err)
	}
}
