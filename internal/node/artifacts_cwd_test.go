package node

import (
	"testing"

	"github.com/k1dory/acpost/internal/store"
)

// An artifact says its chat's directory: what the app reads a reply file's relative links from
// (review round 1).
func TestArtifactsCarryTheirChatsDirectory(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	m := &store.Message{ID: store.NewID(), From: "user:tester", To: "agent:bot", Body: "go", Meta: map[string]string{metaCwd: "/project-b"}}
	if _, _, err := n.st.Insert(ctx, m); err != nil {
		t.Fatal(err)
	}
	if err := n.st.AddChanges(ctx, store.TurnChanges{Msg: m.ID, Agent: "agent:bot", Repo: "/project-b", Files: []store.ChangedFile{{Path: "report.md", Status: "A"}}}); err != nil {
		t.Fatal(err)
	}
	got, err := n.Artifacts(ctx, me, store.ArtifactQuery{})
	if err != nil || len(got.Items) != 1 || got.Items[0].Cwd != "/project-b" {
		t.Fatalf("%+v %v", got.Items, err)
	}
}
