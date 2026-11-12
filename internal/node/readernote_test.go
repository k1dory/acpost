package node

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// The note on what the app renders tells an agent of the components it draws (```ui blocks), and
// stays short: it is in the first prompt of every session the user starts.
func TestReaderNoteNamesTheComponents(t *testing.T) {
	_, ui, ok := strings.Cut(readerNote, "Do not draw what a sentence says as well. ")
	if !ok || !strings.Contains(readerNote, "```ui block") {
		t.Fatalf("the note has no part on ```ui blocks: %q", readerNote)
	}
	for _, typ := range []string{"stats", "chart", "table", "timeline", "kanban", "steps", "choices", "form"} {
		if !strings.Contains(ui, typ+" {") {
			t.Errorf("the note does not describe the %s component", typ)
		}
	}
	if n := len([]rune(ui)); n > 1000 {
		t.Errorf("the part on components is %d characters, want 1000 at most", n)
	}
	if strings.ContainsAny(ui, "<>") {
		t.Error("the part on components has angle brackets: it says no HTML")
	}
	if !strings.HasSuffix(readerNote, "\n\n") {
		t.Error("the note does not end with a blank line before the message")
	}
}

// The note as sessions started before the components were given it is still in turn.ts (the app
// takes it off their prompts read back), to the letter.
func TestOlderReaderNoteStaysInTheDesktop(t *testing.T) {
	const older = "[acpost] The user reads your answers in the acpost app, which renders Markdown, code blocks and Mermaid. " +
		"When a flow, an architecture, a sequence of calls or a state machine is clearer as a picture, draw it in a ```mermaid block " +
		"(flowchart TD/LR or sequenceDiagram; every label in double quotes, A[\"api (HTTP)\"] -->|\"calls\"| B[\"store\"]; no HTML; about 15 nodes at most). " +
		"Do not draw what a sentence says as well.\n\n"
	raw, err := os.ReadFile("../../desktop/frontend/src/lib/turn.ts")
	if err != nil {
		t.Fatal(err)
	}
	ts := string(raw)
	var b strings.Builder
	e := json.NewEncoder(&b)
	e.SetEscapeHTML(false)
	e.Encode(older)
	if !strings.Contains(ts, strings.TrimSpace(b.String())) {
		t.Error("turn.ts no longer has the older note: prompts of earlier sessions would show it")
	}
}
