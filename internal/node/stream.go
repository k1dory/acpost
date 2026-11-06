package node

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/k1dory/acpost/internal/store"
)

// The agent's text as it comes: its chunks are kept as "text" (and "thought") events, a few a
// second, so whoever follows a message's events sees the answer being written. The reply is still
// the turn's text at its end; these events are how it got there.

const (
	streamEvery = 500 * time.Millisecond // pending text is recorded at least this often (at first)
	streamMax   = 4 << 10                // and in events of at most this many bytes of text
	streamJSON  = 12 << 10               // encoded, within the store's event data (16 KiB)
	streamSlow  = 100                    // every this many text events, the interval grows by streamEvery
)

// textStream gathers a turn's text chunks into events, and keeps the turn's events in order: text
// is recorded before the event that follows it.
//
// Text takes at most budget events of the message's limits.max_events_per_message, the rest stays
// for tool calls and permissions: the interval grows as text events add up, and past the budget the
// text is no longer streamed (said once; the reply has it all).
type textStream struct {
	mu     sync.Mutex
	record func(kind string, data any) // records one event; called under mu
	budget int                         // text events this turn may record
	used   int
	kind   string
	buf    strings.Builder
	timer  *time.Timer
	closed bool
}

// add appends a chunk of the agent's text (kind "text") or thinking ("thought").
func (s *textStream) add(kind, text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || text == "" {
		return
	}
	if s.buf.Len() > 0 && s.kind != kind {
		s.flushLocked()
	}
	s.kind = kind
	s.buf.WriteString(text)
	if s.buf.Len() >= streamMax {
		s.flushLocked()
		return
	}
	if s.timer == nil {
		s.timer = time.AfterFunc(streamEvery*time.Duration(1+s.used/streamSlow), s.flush)
	}
}

// event records another event of the turn, after the text that came before it.
func (s *textStream) event(kind string, data any) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked()
	if !s.closed {
		s.record(kind, data)
	}
}

// close records the text still pending; what comes later (after the turn) is not kept.
func (s *textStream) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked()
	s.closed = true
}

func (s *textStream) flush() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.flushLocked()
}

func (s *textStream) flushLocked() {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if s.closed || s.buf.Len() == 0 {
		return
	}
	text := s.buf.String()
	s.buf.Reset()
	if s.used >= s.budget {
		return // past the budget: said once, below
	}
	// In pieces of at most streamMax bytes, cut between characters.
	for text != "" {
		cut := min(len(text), streamMax)
		for cut < len(text) && cut > 0 && !utf8.RuneStart(text[cut]) {
			cut--
		}
		if cut == 0 {
			cut = min(len(text), streamMax)
		}
		// Escaped (quotes, control characters) it may grow up to six times: smaller, then.
		for cut > 1 && len(eventJSON(text[:cut])) > streamJSON {
			cut /= 2
			for cut > 1 && !utf8.RuneStart(text[cut]) {
				cut--
			}
		}
		if s.used++; s.used == s.budget {
			s.record("notice", "the answer is no longer streamed (half of limits.max_events_per_message is for its text); the reply has it all")
			return
		}
		s.record(s.kind, map[string]string{"text": text[:cut]})
		text = text[cut:]
	}
}

// addEvent records an event of a message and wakes whoever follows its events. A turn's events
// (turn) are kept only while the message is not final.
func (n *Node) addEvent(ctx context.Context, id, kind, data string, turn bool) error {
	add := n.st.AddEvent
	if turn {
		add = n.st.AddTurnEvent
	}
	err := add(ctx, id, kind, data)
	if err == nil {
		n.eventsAdded()
	}
	return err
}

// eventsAdded wakes whoever follows events.
func (n *Node) eventsAdded() {
	n.mu.Lock()
	close(n.evChanged)
	n.evChanged = make(chan struct{})
	n.mu.Unlock()
}

func (n *Node) eventChanges() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.evChanged
}

// Follow calls fn with each event of a message after the one numbered after, as they are
// recorded, until the message is final and its events are read; then it returns the message. It
// returns early when ctx ends or fn fails. A message sent to another node has its events there:
// here it is followed to its end.
func (n *Node) Follow(ctx context.Context, p Principal, id string, after int64, fn func(store.Event) error) (store.Message, error) {
	for {
		evs, changed := n.eventChanges(), n.changes()
		m, err := n.Get(ctx, p, id)
		if err != nil {
			return m, err
		}
		// The events are read after the state: the last ones of a final message are not missed.
		for {
			page, err := n.st.Events(ctx, id, after, store.MaxPage)
			if err != nil {
				return m, err
			}
			for _, e := range page {
				if err := fn(e); err != nil {
					return m, err
				}
				after = e.Seq
			}
			if len(page) < store.MaxPage {
				break
			}
		}
		if m.State.Final() {
			return m, nil
		}
		select {
		case <-evs:
		case <-changed:
		case <-time.After(2 * time.Second): // a change made by another path (the hub's copy) is seen anyway
		case <-ctx.Done():
			return m, ctx.Err()
		}
	}
}

// eventJSON is data as an event keeps it: JSON, with < > & as they are.
func eventJSON(data any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(data)
	return strings.TrimSuffix(b.String(), "\n")
}
