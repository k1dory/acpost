package node

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A turn whose lease was lost (reaped, the message queued again) tells the agent it is over
// (session/cancel), so the agent does not go on with it while the next attempt prompts: the SDK
// sends it when Prompt's context ends (acp-go-sdk ClientSideConnection.Prompt), which a lost lease
// ends. Kept as the contract the review asked about.
func TestLostLeaseCancelsTheTurn(t *testing.T) {
	cancels := filepath.Join(t.TempDir(), "cancels")
	n := roundNode(t, map[string]string{"FAKE_CANCELS": cancels}, "lease: 3s")
	m, _, err := n.Send(ctx, me, SendRequest{To: "fake", Body: "sleep 1500", Task: "T"})
	if err != nil {
		t.Fatal(err)
	}
	sid := storedSession(t, n, "task:T")
	for {
		if got, _ := n.st.Get(ctx, m.ID); got.State == store.Running {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond) // in its prompt
	if _, err := n.st.ReapLeases(ctx, time.Now().Add(time.Hour), 5); err != nil {
		t.Fatal(err)
	}
	n.notify() // the turn's watch reads the message again
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if b, _ := os.ReadFile(cancels); strings.Contains(string(b), sid) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the agent was not told the turn was over (no session/cancel)")
}

// explainStart reads what went wrong from the error itself: the shell's exit 127 is a command not
// found, whatever the text around it.
func TestExplainStartByType(t *testing.T) {
	err := exec.Command("sh", "-c", "exit 127").Run()
	if f := explainStart(wrapf("initialize: the agent went away (%w)", err)); f.Code != "agent_start" || !strings.Contains(f.Message, "PATH") {
		t.Fatalf("%+v", f)
	}
	if f := explainStart(wrapf("a text that says exit status 127 is not an exit")); strings.Contains(f.Message, "PATH") {
		t.Fatalf("by the text: %+v", f)
	}
}

// A link without a type or an extension is not taken as text.
func TestLinkWithoutTypeIsOctetStream(t *testing.T) {
	if got := linkType("download", ""); got != "application/octet-stream" {
		t.Fatalf("%q", got)
	}
	if got := linkType("notes.md", ""); !strings.HasPrefix(got, "text/markdown") {
		t.Fatalf("%q", got)
	}
	if got := fileType("empty", "", nil); !strings.HasPrefix(got, "text/plain") {
		t.Fatalf("an empty file: %q", got)
	}
}
