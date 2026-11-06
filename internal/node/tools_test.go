package node

import (
	"encoding/json"
	"strings"
	"testing"

	acp "github.com/coder/acp-go-sdk"
)

func ptr[T any](v T) *T { return &v }

func textContent(s string) acp.ToolCallContent {
	return acp.ToolCallContent{Content: &acp.ToolCallContentContent{Type: "content", Content: acp.TextBlock(s)}}
}

// claude-agent-acp says a command in pieces: an empty call, the command, its description, the
// result. The events say what was run, why, what came out and how it ended.
func TestToolTrackClaude(t *testing.T) {
	turn := &acpTurn{}
	first := turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "t1", Title: "Terminal", Kind: acp.ToolKindExecute, Status: acp.ToolCallStatusPending, RawInput: map[string]any{}})
	if first.Input != nil || first.Title != "Terminal" {
		t.Fatalf("%+v", first)
	}
	e, ok := turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "t1", Title: ptr("echo out1; exit 3"), RawInput: map[string]any{"command": "echo out1; exit 3"}})
	if !ok || string(e.Input) != `{"command":"echo out1; exit 3"}` || e.Title != "echo out1; exit 3" {
		t.Fatalf("input: %v %+v", ok, e)
	}
	_, ok = turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "t1", RawInput: map[string]any{"command": "echo out1; exit 3", "description": "Echo and fail"},
		Content: []acp.ToolCallContent{textContent("Echo and fail")}})
	if ok {
		t.Fatal("an update saying nothing new was an event")
	}
	e, ok = turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "t1", Status: ptr(acp.ToolCallStatusFailed), RawOutput: "Exit code 3\nout1",
		Content: []acp.ToolCallContent{textContent("```\nExit code 3\nout1\n```")}})
	if !ok || e.Status != "failed" || e.Description != "Echo and fail" || e.Output != "Exit code 3\nout1" {
		t.Fatalf("end: %v %+v", ok, e)
	}
	// An edit: the diff, with lines counted.
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "t2", Title: "Edit", Kind: acp.ToolKindEdit})
	e, ok = turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "t2", Content: []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{Path: "/w/f.txt", OldText: ptr("x\na\ny\n"), NewText: "x\nb\nc\ny\n"}}}})
	if !ok || len(e.Edits) != 1 || e.Edits[0].Removed != 1 || e.Edits[0].Added != 2 || e.Edits[0].New != "x\nb\nc\ny\n" {
		t.Fatalf("edit: %v %+v", ok, e)
	}
}

// codex-acp with terminal_output: the command in the call, the output and exit code in _meta.
func TestToolTrackCodex(t *testing.T) {
	turn := &acpTurn{}
	e := turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "c1", Title: "echo out1", Kind: acp.ToolKindExecute, Status: acp.ToolCallStatusInProgress,
		RawInput: map[string]any{"command": "echo out1", "cwd": "/w"}})
	if !strings.Contains(string(e.Input), `"command":"echo out1"`) {
		t.Fatalf("%+v", e)
	}
	e, ok := turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "c1", Status: ptr(acp.ToolCallStatusFailed), Meta: map[string]any{
		"terminal_output": map[string]any{"data": "out1\nerr1\n"}, "terminal_exit": map[string]any{"exit_code": float64(3)}}})
	if !ok || e.Output != "out1\nerr1\n" || e.ExitCode == nil || *e.ExitCode != 3 {
		t.Fatalf("%v %+v", ok, e)
	}
}

// A long output keeps its tail; a big edit is cut, and an event stays within the event size.
func TestToolTrackBounded(t *testing.T) {
	turn := &acpTurn{}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "b", RawInput: map[string]any{"command": strings.Repeat("c", 10000)}})
	big := strings.Repeat("old line\n", 3000)
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "b", Content: []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{Path: "p", OldText: &big, NewText: big + "new\n"}}}})
	e, _ := turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "b", Status: ptr(acp.ToolCallStatusCompleted), RawOutput: strings.Repeat("o", 100000) + "END"})
	b, _ := json.Marshal(e)
	if len(b) > 16<<10 || !strings.HasSuffix(e.Output, "END") || !e.Edits[0].Cut || e.Edits[0].Added != 1 || e.Edits[0].Removed != 0 {
		t.Fatalf("%d bytes, output end %q, edit %+v", len(b), e.Output[len(e.Output)-3:], e.Edits[0].Cut)
	}
}

func TestToolInputReadsAsWritten(t *testing.T) {
	b, ok := inputJSON(map[string]any{"command": "a > b && c < d"})
	if !ok || string(b) != `{"command":"a > b && c < d"}` {
		t.Fatalf("%s", b)
	}
}

// Content is a snapshot: sent again it is not added again, and later raw output replaces earlier.
func TestToolTrackSnapshots(t *testing.T) {
	turn := &acpTurn{}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "s"})
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "s", RawOutput: "partial", Content: []acp.ToolCallContent{textContent("hello")}})
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "s", Content: []acp.ToolCallContent{textContent("hello")}})
	e, _ := turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "s", Status: ptr(acp.ToolCallStatusCompleted), RawOutput: "partial\nresult"})
	if e.Output != "partial\nresult" {
		t.Fatalf("%q", e.Output)
	}
	// Text output of an adapter that sends it while the call runs, then only the status.
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "r"})
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "r", Status: ptr(acp.ToolCallStatusInProgress), Content: []acp.ToolCallContent{textContent("result text")}})
	e, _ = turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "r", Status: ptr(acp.ToolCallStatusCompleted)})
	if e.Output != "result text" || e.Description != "" {
		t.Fatalf("%+v", e)
	}
	for i := range maxTracked + 10 {
		turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: acp.ToolCallId("n" + itoa(i))})
	}
	if len(turn.tools) > maxTracked {
		t.Fatalf("%d calls tracked", len(turn.tools))
	}
}

// Secrets in a call's input and output are masked before they are kept.
func TestToolSecretsMasked(t *testing.T) {
	turn := &acpTurn{}
	e := turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "x", RawInput: map[string]any{
		"command": `curl -H "Authorization: Bearer abcdefghijklmnop" https://bob:hunter22@api.example.com --token s3cr3tvalue && GITHUB_TOKEN=ghp_xxxxxxxx make`,
		"env":     map[string]any{"API_KEY": "k-123", "HOME": "/home/x"},
	}})
	in := string(e.Input)
	for _, secret := range []string{"abcdefghijklmnop", "hunter22", "s3cr3tvalue", "ghp_xxxxxxxx", "k-123"} {
		if strings.Contains(in, secret) {
			t.Fatalf("%s kept: %s", secret, in)
		}
	}
	if !strings.Contains(in, "/home/x") || !strings.Contains(in, "make") {
		t.Fatalf("too much masked: %s", in)
	}
	e, _ = turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "x", Status: ptr(acp.ToolCallStatusCompleted), RawOutput: "export PASSWORD=topsecret1\nok"})
	if strings.Contains(e.Output, "topsecret1") || !strings.Contains(e.Output, "ok") {
		t.Fatalf("%q", e.Output)
	}
}

// Text that grows when encoded (tabs, quotes) still makes an event within the limit, valid JSON.
func TestToolEventFitsEncoded(t *testing.T) {
	turn := &acpTurn{}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "f", Title: strings.Repeat("t", 5000)})
	tabs := strings.Repeat("\t", 8000)
	e, _ := turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "f", Status: ptr(acp.ToolCallStatusCompleted), RawOutput: strings.Repeat("\"", 4000),
		Content: []acp.ToolCallContent{{Diff: &acp.ToolCallContentDiff{Path: "p", OldText: &tabs, NewText: tabs + "x"}}}})
	b := eventJSON(e)
	if len(b) > toolEventMax || !json.Valid([]byte(b)) || len(e.Title) > 512 {
		t.Fatalf("%d bytes, valid %v", len(b), json.Valid([]byte(b)))
	}
}

// The cases of the second review: JSON secrets, secret arrays, masking before the cut, input JSON
// kept valid, output after the end, empty snapshots, a long id.
func TestToolReviewCases(t *testing.T) {
	for _, s := range []string{`{"password":"hunter22"}`, `{"api_key": ["sk-example"]}`, `"db_password" : "p@ss w0rd"`} {
		if r := redact(s); strings.Contains(r, "hunter22") || strings.Contains(r, "sk-example") || strings.Contains(r, "p@ss") {
			t.Fatalf("%s -> %s", s, r)
		}
	}
	if m := maskFields(map[string]any{"api_key": []any{"sk-example"}}).(map[string]any); m["api_key"] != masked {
		t.Fatalf("%v", m)
	}
	turn := &acpTurn{}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: acp.ToolCallId(strings.Repeat("i", 20000)), RawInput: map[string]any{"command": `cmd --token "abc" x`}})
	e := turn.tools[strings.Repeat("i", 20000)].event()
	if !json.Valid(e.Input) || strings.Contains(string(e.Input), "abc") || len(e.ID) > 128 {
		t.Fatalf("input %s, id %d", e.Input, len(e.ID))
	}
	// TOKEN= far from the end: masked before the output is cut.
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "o"})
	e, _ = turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "o", Status: ptr(acp.ToolCallStatusCompleted), RawOutput: "TOKEN=abcdefghij\n" + strings.Repeat("x", toolOutputMax-11)})
	if strings.Contains(e.Output, "abcdefghij") {
		t.Fatalf("token after the cut: %q", e.Output[:40])
	}
	// A result after the end is said; an empty snapshot clears the text.
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "late"})
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "late", Status: ptr(acp.ToolCallStatusCompleted)})
	if e, ok := turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "late", RawOutput: "result"}); !ok || e.Output != "result" {
		t.Fatalf("late output: %v %+v", ok, e)
	}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "c", Content: []acp.ToolCallContent{textContent("old")}})
	if e, _ := turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "c", Content: []acp.ToolCallContent{}, Status: ptr(acp.ToolCallStatusCompleted)}); e.Output != "" {
		t.Fatalf("emptied content kept: %q", e.Output)
	}
}

// The cases of the third review.
func TestToolReviewCases3(t *testing.T) {
	if r := redact(`{"api_key":[["first"],["sk-secret"]]}`); strings.Contains(r, "sk-secret") {
		t.Fatal(r)
	}
	// A secret split across terminal chunks, past a compaction.
	turn := &acpTurn{}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "t"})
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "t", Meta: map[string]any{"terminal_output": map[string]any{"data": strings.Repeat("p", 8200) + "\nBearer abcdefgh"}}})
	e, _ := turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "t", Status: ptr(acp.ToolCallStatusCompleted), Meta: map[string]any{"terminal_output": map[string]any{"data": "ijklmnop\n"}}})
	if strings.Contains(e.Output, "abcdefgh") || strings.Contains(e.Output, "ijklmnop") {
		t.Fatalf("%q", e.Output[len(e.Output)-40:])
	}
	// Past maxTracked ended calls, a new call is still tracked.
	for i := range maxTracked {
		id := acp.ToolCallId("e" + itoa(i))
		turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: id})
		turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: id, Status: ptr(acp.ToolCallStatusCompleted)})
	}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "new"})
	if _, ok := turn.tools["new"]; !ok {
		t.Fatal("a new call not tracked")
	}
	e = fitEvent(toolEvent{ID: "x", Kind: strings.Repeat("k", 20000), Status: strings.Repeat("s", 20000)})
	if len(eventJSON(e)) > toolEventMax {
		t.Fatal("kind/status unbounded")
	}
}

// The cases of the fourth review.
func TestToolReviewCases4(t *testing.T) {
	if r := redact("{\"api_key\":[\n\"sk-secret\"\n]}"); strings.Contains(r, "sk-secret") {
		t.Fatal(r)
	}
	turn := &acpTurn{}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "t"})
	// One long line, no newline before the cut: masked before it is cut.
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "t", Meta: map[string]any{"terminal_output": map[string]any{"data": "TOKEN=" + strings.Repeat("v", termMax) + "\n"}}})
	e, _ := turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "t", Status: ptr(acp.ToolCallStatusCompleted), Meta: map[string]any{"terminal_output": map[string]any{"data": "done\n"}}})
	if strings.Contains(e.Output, "vvvv") || !strings.Contains(e.Output, "done") {
		t.Fatalf("%q", e.Output)
	}
	// A 9 KiB line ending in a newline is not output lost.
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "u"})
	e, _ = turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "u", Status: ptr(acp.ToolCallStatusCompleted), Meta: map[string]any{"terminal_output": map[string]any{"data": strings.Repeat("x", termMax+9000) + "\n"}}})
	if !strings.Contains(e.Output, "xxxx") {
		t.Fatal("output lost")
	}
}

func TestToolReviewCases5(t *testing.T) {
	turn := &acpTurn{}
	turn.trackCall(&acp.SessionUpdateToolCall{ToolCallId: "a"})
	turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "a", Meta: map[string]any{"terminal_output": map[string]any{"data": strings.Repeat("line\n", termMax/5+10) + "{\"api_key\":[\n\"sk-secret\"]}"}}})
	e, _ := turn.trackUpdate(&acp.SessionToolCallUpdate{ToolCallId: "a", Status: ptr(acp.ToolCallStatusCompleted)})
	if strings.Contains(e.Output, "sk-secret") {
		t.Fatalf("%q", e.Output[len(e.Output)-60:])
	}
}
