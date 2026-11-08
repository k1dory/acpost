package node

import (
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A reply with files repeated by its holder (the response was lost) is recorded already: no
// conflict over the names, no refusal for counting the files twice, one copy of each. A
// different answer under the same lease (a file added, another error text) is still a conflict.
func TestReplyWithFilesRepeated(t *testing.T) {
	n := newNode(t, "limits: {max_files: 2}\nagents:\n  bot: {driver: inbox}\n")
	if _, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "review"}); err != nil {
		t.Fatal(err)
	}
	c, ok, err := n.Claim(ctx, me, "bot", time.Second, time.Minute)
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	files := func() []store.File { return []store.File{file("a.md", "a"), file("b.md", "b")} } // max_files reached
	for i := range 3 {
		if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "looks fine", "", "", files()); err != nil {
			t.Fatalf("reply %d: %v", i, err)
		}
	}
	if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "looks fine", "", "", append(files()[:1], file("c.md", "c"))); code(err) != "conflict" {
		t.Fatalf("another file under the same lease: %v", err)
	}
	if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "looks fine", "", "", nil); code(err) != "conflict" {
		t.Fatalf("no files under the same lease: %v", err)
	}
	all, err := n.Files(ctx, me, c.ID)
	if err != nil || len(all) != 2 {
		t.Fatalf("%+v %v", all, err)
	}
}

func TestFailureRepeatedWithAnotherText(t *testing.T) {
	n := newNode(t, "agents:\n  bot: {driver: inbox}\n")
	if _, _, err := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"}); err != nil {
		t.Fatal(err)
	}
	c, _, _ := n.Claim(ctx, me, "bot", time.Second, time.Minute)
	if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "", "failed", "boom", nil); err != nil {
		t.Fatal(err)
	}
	if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "", "failed", "boom", nil); err != nil {
		t.Fatalf("same failure: %v", err)
	}
	if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "", "failed", "bang", nil); code(err) != "conflict" {
		t.Fatalf("another error text: %v", err)
	}
	if err := n.ReplyCoded(ctx, me, c.ID, c.LeaseID, "answer", "", "", []store.File{file("report.txt", "r")}); code(err) != "conflict" {
		t.Fatalf("another outcome: %v", err)
	}
}
