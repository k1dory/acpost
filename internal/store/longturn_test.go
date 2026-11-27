package store

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// A turn of hours: far more commands than the old limit of 1000 events, each with a long output.
// It is followed to its end (no "later events were not kept"), and past a tenth of the limit its
// events are small yet still whole JSON: a tool call keeps its id and its state.
func TestLongTurnFollowedToItsEnd(t *testing.T) {
	s := openTest(t, Options{})
	m := mustInsert(t, s, msg("agent:r", "x"))
	out := strings.Repeat("строка вывода команды\n", 600) // ~13 KiB
	const calls = 1500
	for i := range calls {
		id := fmt.Sprintf("toolu_%04d", i)
		call, _ := json.Marshal(map[string]any{"id": id, "title": "sed -n 1,200p file.go", "kind": "execute", "status": "pending"})
		done, _ := json.Marshal(map[string]any{"id": id, "status": "completed", "content": []any{map[string]any{"type": "content", "content": map[string]any{"type": "text", "text": out}}}})
		if err := s.AddEvent(ctx, m.ID, "tool_call", string(call)); err != nil {
			t.Fatal(err)
		}
		if err := s.AddEvent(ctx, m.ID, "tool_call_update", string(done)); err != nil {
			t.Fatal(err)
		}
	}
	var all []Event
	for after := int64(0); ; {
		evs, err := s.Events(ctx, m.ID, after, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if len(evs) == 0 {
			break
		}
		all = append(all, evs...)
		after = evs[len(evs)-1].Seq
	}
	if len(all) != 2*calls {
		t.Fatalf("%d events kept, want %d", len(all), 2*calls)
	}
	for _, e := range all {
		if e.Kind == "truncated" {
			t.Fatal("a long turn was cut off")
		}
	}
	early, last := all[1], all[len(all)-1]
	if len(early.Data) < 8<<10 {
		t.Fatalf("an early event lost its output: %d bytes", len(early.Data))
	}
	if len(last.Data) > longTurnData {
		t.Fatalf("a late event keeps %d bytes, want at most %d", len(last.Data), longTurnData)
	}
	var d struct{ ID, Status string }
	if err := json.Unmarshal([]byte(last.Data), &d); err != nil || d.ID != fmt.Sprintf("toolu_%04d", calls-1) || d.Status != "completed" {
		t.Fatalf("a late tool call does not read as one: %v %+v", err, d)
	}
}

// Data that is not JSON is still cut at the limit; a cut string never splits a character.
func TestFitEventData(t *testing.T) {
	if got := fitEventData(strings.Repeat("a", 100), 10); got != strings.Repeat("a", 10)+truncMark {
		t.Fatalf("plain data: %q", got)
	}
	got := fitEventData(`{"text":"`+strings.Repeat("я", 3000)+`"}`, 2048)
	var v struct{ Text string }
	if err := json.Unmarshal([]byte(got), &v); err != nil || !strings.HasSuffix(v.Text, truncMark) || !strings.HasPrefix(v.Text, "яя") {
		t.Fatalf("json data: %v %q", err, got[:40])
	}
	if strings.ContainsRune(v.Text, '�') {
		t.Fatal("a character was split")
	}
}
