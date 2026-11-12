package node

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/k1dory/acpost/internal/store"
)

// A chat shared by several agents: a thread whose messages go to more than one of them. Each
// agent keeps a session of its own for the thread, so it never saw what the others were asked and
// answered there. Its next turn in the thread is told: the exchanges finished since it was last
// told, before the message itself.

const (
	// sharedSeen is the store's session key (a namespace of its own, like made:) under which an
	// agent's mark for a thread is kept; it goes with the sessions unused past retention.
	sharedSeen = "seen:"
	// sharedMargin: a message's finish time is taken before its transaction commits, so one that
	// finished a moment before the mark may show up after it. Those are looked at again, and the
	// ones told are remembered by id for this long.
	sharedMargin = 5 * time.Second

	sharedMaxTurns = 30       // exchanges told at once (the latest)
	sharedMaxBody  = 2 << 10  // bytes of a question
	sharedMaxReply = 6 << 10  // bytes of an answer
	sharedMaxTotal = 32 << 10 // bytes of all of it
)

// sharedMark is what an agent was told of a thread so far.
type sharedMark struct {
	At  int64            `json:"at"`            // ms: the latest finish told
	IDs map[string]int64 `json:"ids,omitempty"` // the messages told that finished within sharedMargin of At, and when
}

// othersSaid is what the thread's other agents were asked and answered since this agent was last
// told, as text to go before m's body ("" when nothing), and the mark to keep once the agent has
// it (toldOthers). Only for a thread of this node's own user and agents, and only their messages:
// what a token client or another node put in a thread of the same name is not handed to an agent
// as the conversation. An agent without the preamble (its prompts are the bodies, whole) and one
// that keeps no session between messages are told nothing.
func (a *acpAgent) othersSaid(ctx context.Context, m store.Message) (string, *sharedMark, error) {
	if m.Thread == "" || !knowledgeSender(m) || !a.cfg.PreambleOn() || a.cfg.Session == "message" {
		return "", nil, nil
	}
	st := a.n.st
	var mark sharedMark
	if raw, err := st.Session(ctx, a.addr, sharedSeen+m.Thread); err != nil {
		return "", nil, err
	} else if raw != "" {
		_ = json.Unmarshal([]byte(raw), &mark) // unreadable: as if never told (told again, not lost)
	}
	floor := mark.At - sharedMargin.Milliseconds()
	var news []store.Message
	after := ""
	for {
		page, next, err := st.List(ctx, store.Filter{Thread: m.Thread, After: after})
		if err != nil {
			return "", nil, err
		}
		for _, x := range page {
			if x.ID == m.ID || x.To == a.addr || x.From == a.addr || x.State != store.Done || !localSender(x.From) {
				continue // its own turns it has; what it asked another itself, it was answered
			}
			fin := x.FinishedAt.UnixMilli()
			if _, told := mark.IDs[x.ID]; told || fin < floor {
				continue
			}
			news = append(news, x)
		}
		if next == "" {
			break
		}
		after = next
	}
	if len(news) == 0 {
		return "", nil, nil
	}
	next := sharedMark{At: mark.At, IDs: map[string]int64{}}
	for _, x := range news {
		next.At = max(next.At, x.FinishedAt.UnixMilli())
	}
	keep := next.At - sharedMargin.Milliseconds()
	for id, fin := range mark.IDs {
		if fin >= keep {
			next.IDs[id] = fin
		}
	}
	for _, x := range news {
		if fin := x.FinishedAt.UnixMilli(); fin >= keep {
			next.IDs[x.ID] = fin
		}
	}

	// The latest ones, within the size: the oldest are the ones left out, and that is said.
	var parts []string
	total := 0
	for i := len(news) - 1; i >= 0 && len(parts) < sharedMaxTurns; i-- {
		x, err := st.Get(ctx, news[i].ID)
		if err != nil {
			continue // gone meanwhile (retention)
		}
		part := fmt.Sprintf("[%s asked %s]\n%s\n[%s answered]\n%s\n", x.From, x.To, clipText(x.Body, sharedMaxBody), x.To, clipText(x.Reply, sharedMaxReply))
		if total+len(part) > sharedMaxTotal && len(parts) > 0 {
			break
		}
		total += len(part)
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "", &next, nil
	}
	var b strings.Builder
	b.WriteString("[acpost] This chat is shared with other agents: you see only your own turns of it. Since your last turn here")
	if left := len(news) - len(parts); left > 0 {
		fmt.Fprintf(&b, " (%d earlier exchanges are left out)", left)
	}
	b.WriteString(":\n\n")
	for i := len(parts) - 1; i >= 0; i-- {
		b.WriteString(parts[i])
		b.WriteString("\n")
	}
	b.WriteString("[acpost] That is what the others said. The message to you:\n\n")
	return b.String(), &next, nil
}

// toldOthers keeps the mark of what the agent was told of a thread: after its prompt went through.
func (a *acpAgent) toldOthers(thread string, mark *sharedMark) {
	if mark == nil {
		return
	}
	raw, _ := json.Marshal(mark)
	ctx, done := context.WithTimeout(a.n.ctx, 5*time.Second)
	defer done()
	if err := a.n.st.PutSession(ctx, a.addr, sharedSeen+thread, string(raw)); err != nil {
		a.n.log.Debug("shared chat mark", "err", err)
	}
}

// clipText is s within max bytes, cut at a character, the cut said; "(no text)" for none.
func clipText(s string, max int) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "(no text)"
	}
	if len(s) <= max {
		return s
	}
	return strings.ToValidUTF8(s[:max], "") + " …[cut]"
}
