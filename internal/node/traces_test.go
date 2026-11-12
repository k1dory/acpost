package node

import (
	"testing"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// A conversation's trace: each turn's events as timing, without what they carried — a run of text
// is one span, a call's updates that say nothing new are left out, its command is read from its
// input — and the turns of the agents it asked, under it.
func TestTraces(t *testing.T) {
	n := newNode(t, inboxCfg+"  helper: {driver: inbox}\n")
	root, _, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "fix the build\nand more"})
	add := func(id, kind, data string) {
		t.Helper()
		if err := n.st.AddEvent(ctx, id, kind, data); err != nil {
			t.Fatal(err)
		}
	}
	add(root.ID, "thought", `{"text":"a"}`)
	add(root.ID, "thought", `{"text":"b"}`)
	add(root.ID, "tool_call", `{"id":"c1","kind":"execute","title":"Terminal","status":"pending"}`)
	add(root.ID, "tool_call_update", `{"id":"c1","kind":"execute","title":"Terminal","status":"pending"}`) // nothing new
	add(root.ID, "tool_call_update", `{"id":"c1","status":"pending","input":{"command":"go test ./...","description":"secret words"}}`)
	add(root.ID, "permission_request", `{"request_id":"r1","tool_call_id":"c1","title":"Run go test","options":[]}`)
	add(root.ID, "permission", `{"request_id":"r1","decision":"allow_once","by":"user:x"}`)
	add(root.ID, "tool_call_update", `{"id":"c1","status":"completed","output":"PASS lots of output","exit_code":0}`)
	add(root.ID, "tool_call", `{"id":"c2","kind":"execute","status":"pending","input":{"command":["git","status"]}}`)
	add(root.ID, "tool_call_update", `{"id":"c2","status":"failed"}`)
	add(root.ID, "plan", `["pending: a step"]`)
	add(root.ID, "text", `{"text":"done "}`)
	add(root.ID, "text", `{"text":"now"}`)

	tok := n.issueAgentToken("bot")
	n.setCurrent(tok, root.ID, "")
	p, _ := n.PrincipalForToken(tok)
	child, _, err := n.Send(ctx, p, SendRequest{To: "helper", Body: "look"})
	if err != nil {
		t.Fatal(err)
	}

	got, err := n.Traces(ctx, me, root.ID, 0)
	if err != nil || len(got) != 1 {
		t.Fatalf("%+v %v", got, err)
	}
	th := got[0]
	if th.Thread != root.ID || th.Title != "fix the build" || len(th.Messages) != 2 || th.Cut {
		t.Fatalf("%+v", th)
	}
	if m := th.Messages[1]; m.ID != child.ID || m.Parent != root.ID || m.Depth != 1 || m.To != "agent:helper" {
		t.Fatalf("the subagent's turn: %+v", m)
	}
	evs := th.Messages[0].Events
	kinds := ""
	for _, e := range evs {
		kinds += e.Kind + ":" + e.Status + " "
	}
	want := "thought: tool_call:pending tool_call_update: permission_request: permission:allow_once tool_call_update:completed tool_call:pending tool_call_update:failed text: "
	if kinds != want {
		t.Fatalf("events:\n%s\nwant\n%s", kinds, want)
	}
	if evs[0].End < evs[0].At || evs[2].Command != "go test ./..." || evs[1].Title != "Terminal" || evs[1].Tool != "execute" {
		t.Fatalf("%+v", evs)
	}
	if evs[3].Request != "r1" || evs[3].Call != "c1" || evs[5].Exit == nil || *evs[5].Exit != 0 || evs[6].Command != "git status" {
		t.Fatalf("%+v", evs)
	}

	// Named by the user, the conversation is called so.
	if err := n.st.SetChatTitle(ctx, "thread:"+root.ID, "Build"); err != nil {
		t.Fatal(err)
	}
	if got, _ := n.Traces(ctx, me, "", 0); len(got) != 1 || got[0].Title != "Build" || got[0].Thread != root.ID {
		t.Fatalf("listed: %+v", got)
	}

	if _, err := n.Traces(ctx, me, "msg_none", 0); code(err) != "not_found" {
		t.Fatalf("no such conversation: %v", err)
	}
	if _, err := n.Traces(ctx, Principal{Kind: "client", Name: "c"}, "", 0); code(err) != "refused" {
		t.Fatalf("a client: %v", err)
	}
}

// The answer is bounded: once its events run out, a turn says it is cut.
func TestTraceEventsBudget(t *testing.T) {
	n := newNode(t, inboxCfg)
	m, _, _ := n.Send(ctx, me, SendRequest{To: "bot", Body: "x"})
	for _, id := range []string{"a", "b", "c"} {
		if err := n.st.AddEvent(ctx, m.ID, "tool_call", `{"id":"`+id+`","status":"pending"}`); err != nil {
			t.Fatal(err)
		}
	}
	budget := 2
	evs, cut, err := n.traceEvents(ctx, m.ID, &budget)
	if err != nil || !cut || len(evs) != 2 || budget != 0 {
		t.Fatalf("%+v %v %v %d", evs, cut, err, budget)
	}
}

// A call's title and command are a line, bounded; what is not JSON is skipped.
func TestTracerClips(t *testing.T) {
	var tr tracer
	long := make([]byte, 400)
	for i := range long {
		long[i] = 'x'
	}
	out, kept := tr.add(nil, store.Event{Kind: "tool_call", Data: `{"id":"a","title":"` + string(long) + `","input":{"command":"one\ntwo"}}`})
	if !kept || len([]rune(out[0].Title)) != traceText+1 || out[0].Command != "one" {
		t.Fatalf("%+v", out)
	}
	if out, kept := tr.add([]client.TraceEvent{}, store.Event{Kind: "tool_call", Data: "not json"}); kept || len(out) != 0 {
		t.Fatalf("%+v", out)
	}
}
