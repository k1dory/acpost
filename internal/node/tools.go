package node

import (
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	acp "github.com/coder/acp-go-sdk"
)

// What a turn's tool calls did, kept in its events so a reader sees not only "a tool ran" but
// what the agent tried: the call's input (the command, the path), where it looked, what came out
// (the output's tail, the exit code) and the edits it made. Bounded: an event stays within the
// store's event size.

const (
	toolInputMax  = 2 << 10  // the call's input, as JSON
	toolOutputMax = 4 << 10  // the tail of what it printed
	toolDiffMax   = 8 << 10  // its edits, old and new text together
	toolEditsMax  = 32       // the edits of one call
	termMax       = 64 << 10 // a terminal's output kept as it came, before it is masked and cut
	termRaw       = 2 << 10  // its last lines kept as they came through a cut
	toolLocations = 10
)

// toolEdit is an edit a tool call made: the file, how many lines went and came, and the text
// before and after (cut to fit; Cut says so).
type toolEdit struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
	Old     string `json:"old,omitempty"`
	New     string `json:"new,omitempty"`
	Cut     bool   `json:"cut,omitempty"`
}

// toolEvent is the data of a tool_call or tool_call_update event.
type toolEvent struct {
	ID        string          `json:"id"`
	Title     string          `json:"title,omitempty"`
	Kind      string          `json:"kind,omitempty"`
	Status    string          `json:"status,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	Locations []string        `json:"locations,omitempty"`
	Output    string          `json:"output,omitempty"`
	ExitCode  *int            `json:"exit_code,omitempty"`
	Edits     []toolEdit      `json:"edits,omitempty"`
	// Description is what the agent says the call is for, when it says (claude-agent-acp).
	Description string `json:"description,omitempty"`
	// Name is the agent's own name of the tool, when it says (claude-agent-acp:
	// _meta.claudeCode.toolName — Bash, Edit, mcp__server__tool): what a client that knows the
	// agent's tools chooses its view by. The kind is the protocol's, and coarser.
	Name string `json:"name,omitempty"`
}

// toolTrack is a tool call as its updates made it so far (an agent sends what it knows in pieces:
// claude-agent-acp the command and its description in updates before the result). An update's
// content and raw output are the call's as they are now (ACP: they replace what came before); a
// terminal's output (codex-acp's terminal_output) comes in chunks.
type toolTrack struct {
	toolEvent
	text     string          // the call's text content, as last sent
	raw      string          // its raw output, as last sent
	term     strings.Builder // its terminal's output so far (the tail kept)
	hadInput bool
	ended    bool
	full     []fileEdit // its edits whole, as last sent and not yet taken for what the turn changed (changes.go)
	fullMore bool       // more edits than kept
}

// fileEdit is an edit as the agent reported it: the file and its text before and after, whole
// (cut: too long to keep; the lines are still counted).
type fileEdit struct {
	path           string
	old, new       string
	created        bool // the agent gave no text before, or says it added the file: a new file
	deleted        bool // the agent says it removed the file (codex-acp: _meta.kind "delete")
	added, removed int
	cut            bool
}

const (
	editKeepMax  = 512 << 10 // the text of one call's edits kept whole
	editsKeepMax = 4 << 20   // and of a turn's
	callEditsMax = 500       // the edits of one call kept (more: said as cut)
	editsMax     = 2000      // and of a turn
)

// take makes a completed call's edits the turn's: what the call says it edited now, in the place
// of what it said before (an agent may send the diff after the status: the last word counts). A
// failed or cancelled call edited nothing. Called with t.mu held.
func (t *acpTurn) take(tr *toolTrack) {
	if t.edited == nil {
		t.edited = map[string][]fileEdit{}
	}
	was, had := t.edited[tr.ID]
	// A call whose edits are held completed (it may be tracked no longer: its status is not asked).
	if tr.full == nil || (!had && tr.Status != string(acp.ToolCallStatusCompleted)) {
		return
	}
	full := tr.full
	tr.full = nil // taken: the same content is not taken twice
	t.pendingBytes -= editBytes(full)
	if !had && (len(full) == 0 || t.editedCount >= editsMax) {
		// Nothing to keep, or no room: the turn holds no more of calls than it holds edits.
		t.editedMore = t.editedMore || len(full) > 0
		return
	}
	for _, e := range was {
		t.editedBytes -= len(e.old) + len(e.new)
	}
	t.editedCount -= len(was)
	if len(full) == 0 { // the call says now it edited nothing
		delete(t.edited, tr.ID)
		t.editedOrder = slices.DeleteFunc(t.editedOrder, func(id string) bool { return id == tr.ID })
		return
	}
	kept := make([]fileEdit, 0, len(full))
	for _, e := range full {
		if t.editedCount+len(kept) >= editsMax {
			t.editedMore = true
			break
		}
		if t.editedBytes+t.pendingBytes+len(e.old)+len(e.new) > editsKeepMax {
			e.old, e.new, e.cut = "", "", true
		}
		t.editedBytes += len(e.old) + len(e.new)
		kept = append(kept, e)
	}
	t.editedCount += len(kept)
	t.editedMore = t.editedMore || tr.fullMore
	t.edited[tr.ID] = kept
	if !had {
		t.editedOrder = append(t.editedOrder, tr.ID)
	}
}

func editBytes(edits []fileEdit) int {
	n := 0
	for _, e := range edits {
		n += len(e.old) + len(e.new)
	}
	return n
}

// hold counts the texts a call's edits wait with (was: before its last update) in the turn's
// budget, which is one for the edits taken and those waiting for their call to end: past it they
// wait without their text. A failed call edited nothing: its edits go. A call the turn does not
// track (kept false) waits with nothing. Called with t.mu held.
func (t *acpTurn) hold(tr *toolTrack, was int, kept bool) {
	if tr.Status == string(acp.ToolCallStatusFailed) || !kept {
		tr.full = nil
	}
	now := editBytes(tr.full)
	if t.editedBytes+t.pendingBytes-was+now > editsKeepMax {
		for i := range tr.full {
			tr.full[i].old, tr.full[i].new, tr.full[i].cut = "", "", true
		}
		now = 0
	}
	t.pendingBytes += now - was
}

// edits is what the turn's completed tool calls edited, in the order the calls completed; more
// says some were not kept (past the turn's or a call's count).
func (t *acpTurn) edits() (out []fileEdit, more bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, id := range t.editedOrder {
		out = append(out, t.edited[id]...)
	}
	return out, t.editedMore
}

// maxTracked bounds the calls of a turn tracked at once (a call is let go when it ends).
const maxTracked = 256

// trackCall starts a tool call; its event.
func (t *acpTurn) trackCall(u *acp.SessionUpdateToolCall) toolEvent {
	t.mu.Lock()
	defer t.mu.Unlock()
	tr := &toolTrack{toolEvent: toolEvent{ID: string(u.ToolCallId), Title: u.Title, Kind: string(u.Kind), Status: string(u.Status)}}
	tr.merge(u.RawInput, u.Locations, u.Content, u.RawOutput, u.Meta)
	tr.hadInput = tr.Input != nil
	tr.ended = tr.Status == string(acp.ToolCallStatusCompleted) || tr.Status == string(acp.ToolCallStatusFailed)
	if t.tools == nil {
		t.tools = map[string]*toolTrack{}
	}
	if len(t.tools) >= maxTracked {
		for id, old := range t.tools { // room for it: a call that ended goes
			if old.ended {
				t.pendingBytes -= editBytes(old.full)
				delete(t.tools, id)
				break
			}
		}
	}
	kept := len(t.tools) < maxTracked
	if prev := t.tools[tr.ID]; prev != nil { // the same call started again: the new one stands
		t.pendingBytes -= editBytes(prev.full)
		kept = true
	}
	if kept {
		t.tools[tr.ID] = tr
	}
	t.pendingBytes += editBytes(tr.full) // counted first: take moves it to what the turn holds
	t.take(tr)
	t.hold(tr, editBytes(tr.full), kept)
	return tr.event()
}

// trackUpdate merges an update into its call; its event when it is worth one: the call's input
// known at last, an edit, the call's end.
func (t *acpTurn) trackUpdate(u *acp.SessionToolCallUpdate) (toolEvent, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tr := t.tools[string(u.ToolCallId)]
	kept := tr != nil
	if tr == nil { // a call not seen (or past maxTracked): what this update says
		tr = &toolTrack{toolEvent: toolEvent{ID: string(u.ToolCallId)}}
	}
	was := editBytes(tr.full)
	if u.Title != nil && *u.Title != "" {
		tr.Title = *u.Title
	}
	if u.Kind != nil {
		tr.Kind = string(*u.Kind)
	}
	if u.Status != nil {
		tr.Status = string(*u.Status)
	}
	edits, before := len(tr.Edits), tr.output()
	tr.merge(u.RawInput, u.Locations, u.Content, u.RawOutput, u.Meta)
	ends := tr.Status == string(acp.ToolCallStatusCompleted) || tr.Status == string(acp.ToolCallStatusFailed)
	newInput := !tr.hadInput && tr.Input != nil
	tr.hadInput = tr.hadInput || newInput
	// Its end, and after it what changes its output still (a late result).
	worth := (ends && !tr.ended) || newInput || len(tr.Edits) != edits || (tr.ended && tr.output() != before)
	tr.ended = tr.ended || ends
	// What the call waits with is counted first (take moves it to what the turn holds).
	t.pendingBytes += editBytes(tr.full) - was
	t.take(tr)
	t.hold(tr, editBytes(tr.full), kept)
	return tr.event(), worth
}

// output is what the call printed, as it stands: the terminal's, else the raw output, else its
// text; its secrets masked.
func (tr *toolTrack) output() string {
	if tr.term.Len() > 0 {
		return redact(tr.term.String())
	}
	if tr.raw != "" {
		return tr.raw
	}
	return stripFence(tr.text)
}

// event is the call as an event: its output the terminal's, else the raw output, else its text;
// secrets masked; the whole within toolEventMax encoded.
func (tr *toolTrack) event() toolEvent {
	e := tr.toolEvent
	e.Edits = append([]toolEdit(nil), tr.Edits...)
	e.Output = tail(tr.output(), toolOutputMax) // masked before it was cut
	return fitEvent(e)
}

// merge takes what an update says.
func (tr *toolTrack) merge(input any, locs []acp.ToolCallLocation, content []acp.ToolCallContent, output any, meta map[string]any) {
	if cc, ok := meta["claudeCode"].(map[string]any); ok {
		if name, ok := cc["toolName"].(string); ok && name != "" {
			tr.Name = name
		}
	}
	if b, ok := inputJSON(input); ok {
		tr.Input = b
		if m, ok := input.(map[string]any); ok {
			if d, ok := m["description"].(string); ok && d != "" {
				tr.Description = d
			}
		}
	}
	if len(locs) > 0 {
		tr.Locations = tr.Locations[:0]
		for _, l := range locs {
			if len(tr.Locations) == toolLocations {
				break
			}
			s := l.Path
			if l.Line != nil {
				s += ":" + itoa(*l.Line)
			}
			tr.Locations = append(tr.Locations, s)
		}
	}
	// Content replaces content: its text (but the agent's own description of the call, which
	// claude-agent-acp sends as content too) and its edits, each when the update has some.
	// Edits stay when an update brings text alone: claude-agent-acp ends an edit with its result
	// text, its diff sent before (ACP would have them replaced; the edit was made all the same).
	var text strings.Builder
	var edits []toolEdit
	var full []fileEdit
	fullMore := false
	room, keep := toolDiffMax, editKeepMax
	for _, c := range content {
		switch {
		case c.Content != nil && c.Content.Content.Text != nil:
			if t := c.Content.Content.Text.Text; t != tr.Description {
				text.WriteString(t)
			}
		case c.Diff != nil:
			// The event shows the first of them, cut to its room; what the turn changed takes them
			// all, within its own bounds.
			added, removed := 0, 0
			if len(edits) < toolEditsMax {
				ed, used := editOf(c.Diff, room)
				room -= used
				edits = append(edits, ed)
				added, removed = ed.Added, ed.Removed
			} else {
				old := ""
				if c.Diff.OldText != nil {
					old = *c.Diff.OldText
				}
				removed, added = lineChange(old, c.Diff.NewText)
			}
			if len(full) >= callEditsMax {
				fullMore = true
				continue
			}
			kind, _ := c.Diff.Meta["kind"].(string) // codex-acp: add, delete, update
			fe := fileEdit{path: c.Diff.Path, new: c.Diff.NewText, created: kind == "add" || (kind == "" && c.Diff.OldText == nil), deleted: kind == "delete",
				added: added, removed: removed}
			if c.Diff.OldText != nil {
				fe.old = *c.Diff.OldText
			}
			if len(fe.old)+len(fe.new) > keep {
				fe.old, fe.new, fe.cut = "", "", true
			} else {
				fe.old, fe.new = strings.Clone(fe.old), strings.Clone(fe.new)
			}
			keep -= len(fe.old) + len(fe.new)
			full = append(full, fe)
		}
	}
	if content != nil { // sent, even empty: the text as it is now
		tr.text = strings.Clone(tail(redact(text.String()), 2*toolOutputMax))
	}
	if len(edits) > 0 || (content != nil && len(content) == 0) {
		tr.Edits = edits
	}
	// What the turn changed takes the call's edits as last said: an update with edits, or one
	// that says there are none (content sent, empty). Text alone leaves them, as above.
	if len(full) > 0 {
		tr.full, tr.fullMore = full, fullMore
	} else if content != nil && len(content) == 0 {
		tr.full, tr.fullMore = []fileEdit{}, false
	}
	if output != nil { // sent, even empty: the raw output as it is now
		s, code := outputOf(output)
		tr.raw = strings.Clone(tail(redact(s), 2*toolOutputMax))
		if code != nil {
			tr.ExitCode = code
		}
	}
	// codex-acp, with the terminal_output extension: the command's output in chunks, its exit
	// code. Kept as it came (in memory only), masked when read; past termMax masked whole and cut
	// to its tail, but for its last lines (termRaw, from a line's start) kept as they are: a secret
	// there may go on in the next chunk, or span lines, and is masked whole when read.
	if to, ok := meta["terminal_output"].(map[string]any); ok {
		if d, ok := to["data"].(string); ok {
			tr.term.WriteString(d)
			if tr.term.Len() > termMax {
				all := tr.term.String()
				open := ""
				if i := strings.LastIndexByte(all[:len(all)-min(termRaw, len(all))], '\n'); i >= 0 && len(all)-i-1 <= toolOutputMax {
					open = all[i+1:]
				}
				kept := tail(redact(all[:len(all)-len(open)]), toolOutputMax) + open
				tr.term.Reset()
				tr.term.WriteString(kept)
			}
		}
	}
	if te, ok := meta["terminal_exit"].(map[string]any); ok {
		if f, ok := te["exit_code"].(float64); ok {
			c := int(f)
			tr.ExitCode = &c
		}
	}
}

// inputJSON is a call's input as JSON, its secrets masked, cut to toolInputMax (then a string);
// not for an empty one.
func inputJSON(input any) (json.RawMessage, bool) {
	if input == nil {
		return nil, false
	}
	b := []byte(eventJSON(maskFields(input))) // < > & as they are: a command reads as written
	if string(b) == "null" || string(b) == "{}" || string(b) == `""` || len(b) == 0 {
		return nil, false
	}
	if len(b) <= toolInputMax {
		return b, true
	}
	return json.RawMessage(eventJSON(cutString(string(b), toolInputMax-64) + "…")), true
}

// stripFence is text without the code fence an agent wraps a tool's output in.
func stripFence(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "```") || !strings.HasSuffix(t, "```") || len(t) < 6 {
		return s
	}
	t = strings.TrimSuffix(t, "```")
	if i := strings.IndexByte(t, '\n'); i >= 0 {
		return t[i+1:]
	}
	return ""
}

// toolEventMax bounds a tool event encoded (the store keeps 16 KiB of an event's data).
const toolEventMax = 14 << 10

// fitEvent cuts what an event says until it fits toolEventMax encoded: names first to a sane
// length, then the edits' text, the output, the input.
func fitEvent(e toolEvent) toolEvent {
	e.ID, e.Kind, e.Status, e.Name = cutString(e.ID, 128), cutString(e.Kind, 64), cutString(e.Status, 64), cutString(e.Name, 128)
	e.Title, e.Description = cutString(redact(e.Title), 512), cutString(redact(e.Description), 512)
	for i := range e.Locations {
		e.Locations[i] = cutString(e.Locations[i], 512)
	}
	for i := range e.Edits {
		e.Edits[i].Path = cutString(e.Edits[i].Path, 512)
	}
	for size := len(eventJSON(e)); size > toolEventMax; size = len(eventJSON(e)) {
		switch {
		case editText(e.Edits) > 0:
			for i := range e.Edits {
				ed := &e.Edits[i]
				ed.Old, ed.New, ed.Cut = cutString(ed.Old, len(ed.Old)/2), cutString(ed.New, len(ed.New)/2), true
			}
		case len(e.Output) > 64:
			e.Output = tail(e.Output, len(e.Output)/2)
		case len(e.Input) > 64:
			e.Input = json.RawMessage(eventJSON(cutString(string(e.Input), len(e.Input)/2) + "…"))
		case len(e.Edits) > 0 || len(e.Locations) > 0:
			e.Edits, e.Locations = nil, nil
		default: // names alone: shorter, then gone
			e.Title, e.Description, e.Output, e.Input = cutString(e.Title, 64), cutString(e.Description, 64), "", nil
			if len(eventJSON(e)) > toolEventMax {
				e.Title, e.Description = "", ""
			}
			return e
		}
	}
	return e
}

func editText(edits []toolEdit) int {
	n := 0
	for _, ed := range edits {
		n += len(ed.Old) + len(ed.New)
	}
	return n
}

// editOf is an edit of a diff, its text cut to room; used is how much of room it took.
func editOf(d *acp.ToolCallContentDiff, room int) (toolEdit, int) {
	old := ""
	if d.OldText != nil {
		old = *d.OldText
	}
	ed := toolEdit{Path: d.Path}
	ed.Removed, ed.Added = lineChange(old, d.NewText)
	ed.Old, ed.New = redact(old), redact(d.NewText)
	if len(ed.Old)+len(ed.New) <= room {
		return ed, len(ed.Old) + len(ed.New)
	}
	old, d = ed.Old, &acp.ToolCallContentDiff{NewText: ed.New}
	ed.Cut = true
	half := max(room/2, 0)
	ed.Old, ed.New = strings.Clone(cutString(old, half)), strings.Clone(cutString(d.NewText, half))
	return ed, len(ed.Old) + len(ed.New)
}

// lineChange counts the lines an edit removed and added: what is left of each side once the
// lines they start and end with alike are set aside (exact for one changed stretch).
func lineChange(old, new string) (removed, added int) {
	a, b := lines(old), lines(new)
	for len(a) > 0 && len(b) > 0 && a[0] == b[0] {
		a, b = a[1:], b[1:]
	}
	for len(a) > 0 && len(b) > 0 && a[len(a)-1] == b[len(b)-1] {
		a, b = a[:len(a)-1], b[:len(b)-1]
	}
	return len(a), len(b)
}

func lines(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(s, "\n"), "\n")
}

// outputOf reads what a tool printed and its exit code from the agent's raw output, as the
// adapters give it: a string, or an object with one of the usual fields.
func outputOf(raw any) (string, *int) {
	switch v := raw.(type) {
	case string:
		return v, nil
	case map[string]any:
		var text string
		for _, k := range []string{"formatted_output", "aggregated_output", "output", "stdout", "content"} {
			if s, ok := v[k].(string); ok && s != "" {
				text = s
				break
			}
		}
		if s, ok := v["stderr"].(string); ok && s != "" && text != s {
			text = strings.TrimRight(text, "\n") + "\n" + s
		}
		for _, k := range []string{"exit_code", "exitCode", "code"} {
			if f, ok := v[k].(float64); ok {
				c := int(f)
				return strings.TrimLeft(text, "\n"), &c
			}
		}
		return strings.TrimLeft(text, "\n"), nil
	}
	return "", nil
}

// tail is the end of s, at most max bytes, cut between characters.
func tail(s string, max int) string {
	if len(s) <= max {
		return s
	}
	i := len(s) - max
	for i < len(s) && !utf8.RuneStart(s[i]) {
		i++
	}
	return "…" + s[i:]
}

// cutString is the start of s, at most max bytes, cut between characters.
func cutString(s string, max int) string {
	if len(s) <= max {
		return s
	}
	for max > 0 && !utf8.RuneStart(s[max]) {
		max--
	}
	return s[:max]
}

func itoa(n int) string { return strconv.Itoa(n) }
