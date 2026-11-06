package node

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/k1dory/acpost/internal/store"
)

type recorded struct{ kind, text string }

func newRecorder() (*textStream, func() []recorded) {
	var mu sync.Mutex
	var got []recorded
	s := &textStream{record: func(kind string, data any) {
		mu.Lock()
		defer mu.Unlock()
		text := ""
		if m, ok := data.(map[string]string); ok {
			text = m["text"]
		}
		got = append(got, recorded{kind, text})
	}}
	return s, func() []recorded { mu.Lock(); defer mu.Unlock(); return append([]recorded(nil), got...) }
}

// Chunks come together into few events, in order with the events between them.
func TestTextStreamOrderAndCoalescing(t *testing.T) {
	s, got := newRecorder()
	s.budget = 100
	for i := 0; i < 100; i++ {
		s.add("text", "a")
	}
	s.add("thought", "t")
	s.event("tool_call", nil)
	s.add("text", "b")
	s.close()
	s.add("text", "late")
	want := []recorded{{"text", strings.Repeat("a", 100)}, {"thought", "t"}, {"tool_call", ""}, {"text", "b"}}
	if fmt.Sprint(got()) != fmt.Sprint(want) {
		t.Fatalf("%v", got())
	}
}

// A large chunk is cut into events of at most streamMax bytes, between characters; text pending
// without more chunks is recorded on its own after streamEvery.
func TestTextStreamCutsAndFlushes(t *testing.T) {
	s, got := newRecorder()
	s.budget = 100
	big := strings.Repeat("я", streamMax) // 2 bytes each
	s.add("text", big)
	var joined strings.Builder
	for _, r := range got() {
		if len(r.text) > streamMax || !utf8.ValidString(r.text) {
			t.Fatalf("event of %d bytes, valid %v", len(r.text), utf8.ValidString(r.text))
		}
		joined.WriteString(r.text)
	}
	if joined.String() != big {
		t.Fatal("text lost in the cut")
	}
	n := len(got())
	s.add("text", "tail")
	time.Sleep(streamEvery + 300*time.Millisecond)
	if g := got(); len(g) != n+1 || g[n].text != "tail" {
		t.Fatalf("pending text not flushed by time: %v", g[n:])
	}
	s.close()
}

// The agent's text reaches a follower while the turn runs, a few events, then the end.
func TestFollowStreamsTheTurn(t *testing.T) {
	n := newNode(t, fmt.Sprintf("agents:\n  fake:\n    driver: acp\n    command: [%q]\n    preamble: false\n    env: {ACPOST_FAKE_AGENT: \"1\"}\n", os.Args[0]))
	m, _, err := n.Send(ctx, me, SendRequest{To: "agent:fake", Body: "stream 12 150"})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	var firstText time.Duration
	var text strings.Builder
	var kinds []string
	final, err := n.Follow(ctx, me, m.ID, 0, func(e store.Event) error {
		kinds = append(kinds, e.Kind)
		if e.Kind == "text" {
			if firstText == 0 {
				firstText = time.Since(start)
			}
			var d map[string]string
			json.Unmarshal([]byte(e.Data), &d)
			text.WriteString(d["text"])
		}
		return nil
	})
	if err != nil || final.State != store.Done {
		t.Fatalf("%v %v", final.State, err)
	}
	total := time.Since(start)
	if text.String() != final.Reply || !strings.HasPrefix(final.Reply, "w1 w2") {
		t.Fatalf("streamed %q, reply %q", text.String(), final.Reply)
	}
	if kinds[0] != "thought" {
		t.Fatalf("kinds %v", kinds)
	}
	textEvents := strings.Count(strings.Join(kinds, " "), "text")
	if textEvents < 2 || textEvents > 8 {
		t.Fatalf("%d text events for a 1.8 s answer: %v", textEvents, kinds)
	}
	if firstText > total-500*time.Millisecond {
		t.Fatalf("the first text came at %v of %v: not while the turn ran", firstText, total)
	}
	// Following again from an event on reads only what came after it.
	var after []string
	if _, err := n.Follow(ctx, me, m.ID, 2, func(e store.Event) error { after = append(after, e.Kind); return nil }); err != nil || len(after) != len(kinds)-2 {
		t.Fatalf("after 2: %v %v", after, err)
	}
}

// Text takes its budget of events and no more; past it one notice says so.
func TestTextStreamBudget(t *testing.T) {
	s, got := newRecorder()
	s.budget = 3
	for i := 0; i < 10; i++ {
		s.add("text", strings.Repeat("x", streamMax))
	}
	s.close()
	g := got()
	if len(g) != 3 || g[0].kind != "text" || g[1].kind != "text" || g[2].kind != "notice" {
		t.Fatalf("%v", g)
	}
}

// Text that grows when escaped still fits an event whole: valid JSON, nothing cut by the store.
func TestTextStreamEscapedFits(t *testing.T) {
	for _, chunk := range []string{strings.Repeat("&", streamMax), strings.Repeat("\x01", streamMax), strings.Repeat(`"`, streamMax)} {
		var joined strings.Builder
		s := &textStream{budget: 100, record: func(kind string, data any) {
			b := eventJSON(data)
			var d map[string]string
			if len(b) > 16<<10 || json.Unmarshal([]byte(b), &d) != nil {
				t.Fatalf("event of %d bytes, valid JSON %v", len(b), json.Valid([]byte(b)))
			}
			joined.WriteString(d["text"])
		}}
		s.add("text", chunk)
		s.close()
		if joined.String() != chunk {
			t.Fatalf("text lost: %d of %d bytes", joined.Len(), len(chunk))
		}
	}
	if b := eventJSON(map[string]string{"text": "a<b&c"}); b != `{"text":"a<b&c"}` {
		t.Fatal(b)
	}
}
