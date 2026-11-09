package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A repeat is compared with the files as they were recorded: sent again without a type, it
// matches the type filled in the first time; another type, or a link that claims another size,
// is another answer under the same lease and a conflict, not a success that keeps the old one.
func TestReplyRepeatComparesFileMetadata(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	if _, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "review"}); err != nil {
		t.Fatal(err)
	}
	c, ok, err := n.Claim(ctx, me, "bot", time.Second, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	files := func() []store.File {
		return []store.File{file("notes.md", "# notes"), {Name: "build.log", URI: "https://example.com/build.log", Size: 10}}
	}
	for i := range 2 {
		if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "done", "", "", files()); err != nil {
			t.Fatalf("reply %d without types: %v", i, err)
		}
	}
	all, err := n.Files(ctx, me, c.ID)
	if err != nil || len(all) != 2 {
		t.Fatalf("%+v %v", all, err)
	}
	stored := map[string]store.File{}
	for _, f := range all {
		stored[f.Name] = f
	}
	typed := files()
	typed[0].Type, typed[1].Type = stored["notes.md"].Type, stored["build.log"].Type
	if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "done", "", "", typed); err != nil {
		t.Fatalf("the repeat with the types recorded: %v", err)
	}
	other := files()
	other[0].Type = "application/octet-stream"
	if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "done", "", "", other); code(err) != "conflict" {
		t.Fatalf("another type: %v", err)
	}
	bigger := files()
	bigger[1].Size = 11
	if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "done", "", "", bigger); code(err) != "conflict" {
		t.Fatalf("a link of another size: %v", err)
	}
	if got, _ := n.Files(ctx, me, c.ID); len(got) != 2 {
		t.Fatalf("%+v", got)
	} else {
		for _, f := range got {
			if was := stored[f.Name]; f.Type != was.Type || f.Size != was.Size || f.SHA256 != was.SHA256 || f.URI != was.URI {
				t.Fatalf("a file changed: %+v, was %+v", f, stored[f.Name])
			}
		}
	}
}
