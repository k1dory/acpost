package node

import (
	"encoding/json"
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
	full     []fileEdit // its edits whole, for what the turn changed (changes.go); taken when it completes
	taken    bool
}

// fileEdit is an edit as the agent reported it: the file and its text before and after, whole
// (cut: too long to keep; the lines are still counted).
type fileEdit struct {
	path           string
	old, new       string
	created        bool // the agent gave no text before: a new file
	added, removed int
	cut            bool
}

const (
	editKeepMax  = 512 << 10 // the text of one call's edits kept whole
	editsKeepMax = 4 << 20   // and of a turn's
	editsMax     = 2000      // the edits of a turn kept
)

// take moves a completed call's edits to the turn's (once: a failed or cancelled call edited
// nothing). Called with t.mu held.
func (t *acpTurn) take(tr *toolTrack) {
	if tr.taken || tr.Status != string(acp.ToolCallStatusCompleted) {
		return
	}
	tr.taken = true
	for _, e := range tr.full {
		if len(t.edited) >= editsMax {
			break
		}
		if t.editedBytes+len(e.old)+len(e.new) > editsKeepMax {
			e.old, e.new, e.cut = "", "", true
		}
		t.editedBytes += len(e.old) + len(e.new)
		t.edited = append(t.edited, e)
	}
	tr.full = nil
}

// edits is what the turn's completed tool calls edited, in order.
func (t *acpTurn) edits() []fileEdit {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]fileEdit(nil), t.edited...)
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
				delete(t.tools, id)
				break
			}
		}
	}
	if len(t.tools) < maxTracked {
		t.tools[tr.ID] = tr
	}
	t.take(tr)
	return tr.event()
}

// trackUpdate merges an update into its call; its event when it is worth one: the call's input
// known at last, an edit, the call's end.
func (t *acpTurn) trackUpdate(u *acp.SessionToolCallUpdate) (toolEvent, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	tr := t.tools[string(u.ToolCallId)]
	if tr == nil { // a call not seen (or past maxTracked): what this update says
		tr = &toolTrack{toolEvent: toolEvent{ID: string(u.ToolCallId)}}
	}
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
	t.take(tr)
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
	room, keep := toolDiffMax, editKeepMax
	for _, c := range content {
		switch {
		case c.Content != nil && c.Content.Content.Text != nil:
			if t := c.Content.Content.Text.Text; t != tr.Description {
				text.WriteString(t)
			}
		case c.Diff != nil && len(edits) < toolEditsMax:
			ed, used := editOf(c.Diff, room)
			room -= used
			edits = append(edits, ed)
			fe := fileEdit{path: c.Diff.Path, new: c.Diff.NewText, created: c.Diff.OldText == nil, added: ed.Added, removed: ed.Removed}
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
		if !tr.taken {
			tr.full = full
		}
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
	e.ID, e.Kind, e.Status = cutString(e.ID, 128), cutString(e.Kind, 64), cutString(e.Status, 64)
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
