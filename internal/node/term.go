package node

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Terminals: a shell on the node's machine, in an agent's working directory or another, for the
// node's user (an app's terminal panel). Its output is kept (the last termKeep bytes) with byte
// offsets, so a reader comes back where it was; it goes when closed, or termLinger after its
// shell ends.

const (
	termKeep     = 256 << 10
	termLinger   = 10 * time.Minute
	maxTerms     = 32
	termInputMax = 64 << 10
)

// Terminal is a terminal as the API shows it.
type Terminal struct {
	ID       string    `json:"id"`
	Cwd      string    `json:"cwd"`
	Agent    string    `json:"agent,omitempty"`
	Started  time.Time `json:"started"`
	Running  bool      `json:"running"`
	ExitCode *int      `json:"exit_code,omitempty"`
	Size     int64     `json:"size"` // output bytes so far: the offset the next byte will have
}

// console is a shell on a terminal of its own: what it prints is read, what is typed is written.
type console interface {
	io.ReadWriteCloser
	Resize(cols, rows int) error
	Wait() int // the shell's end: its exit code (0 when it has none to tell)
	Kill()
}

// errNoConsole is a system this node gives no terminals on.
var errNoConsole = errors.New("this node's system has no terminals here yet")

type term struct {
	Terminal
	con    console
	mu     sync.Mutex
	buf    []byte // the last termKeep bytes of output
	base   int64  // the offset of buf[0]
	more   chan struct{}
	done   chan struct{} // the shell has ended and its output is read
	closed chan struct{} // closed by the user: no lingering
	once   sync.Once
	in     chan []byte // input on its way to the shell (a terminal's fd blocks: no deadlines)
	queued int         // its bytes, under mu, within termQueue
}

// termQueue bounds the input waiting for a shell that does not read it.
const termQueue = 256 << 10

type terms struct {
	mu   sync.Mutex
	all  map[string]*term
	open int // terminals and starts under way, within maxTerms
}

// TermRequest opens a terminal: in an agent's working directory, or Cwd, else the home directory.
type TermRequest struct {
	Agent string `json:"agent,omitempty"`
	Cwd   string `json:"cwd,omitempty"`
	Cols  int    `json:"cols,omitempty"`
	Rows  int    `json:"rows,omitempty"`
}

func termAllowed(p Principal) error {
	if p.Kind != "user" {
		return errf("refused", "terminals are for the node's user")
	}
	return nil
}

// OpenTerminal starts a shell in a terminal.
func (n *Node) OpenTerminal(ctx context.Context, p Principal, r TermRequest) (Terminal, error) {
	if err := termAllowed(p); err != nil {
		return Terminal{}, err
	}
	dir := r.Cwd
	if r.Agent != "" {
		a := n.acp[strings.TrimPrefix(r.Agent, "agent:")]
		if a == nil {
			return Terminal{}, errh("not_found", "`acpost agents` lists them", "no ACP agent %s here", r.Agent)
		}
		dir = (&acpSlot{a: a}).cwd()
	}
	if dir == "" {
		dir, _ = os.UserHomeDir()
	}
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return Terminal{}, errf("invalid", "%s is not a directory here", dir)
	}
	n.terms.mu.Lock()
	if n.terms.all == nil {
		n.terms.all = map[string]*term{}
	}
	if n.terms.open >= maxTerms {
		n.terms.mu.Unlock()
		return Terminal{}, errf("refused", "%d terminals are open: close one", maxTerms)
	}
	n.terms.open++ // its place, held from here
	n.terms.mu.Unlock()
	release := func() {
		n.terms.mu.Lock()
		n.terms.open--
		n.terms.mu.Unlock()
	}

	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ACPOST_") && !strings.HasPrefix(kv, "TERM=") {
			env = append(env, kv)
		}
	}
	env = append(env, "TERM=xterm-256color", "COLORTERM=truecolor")
	con, err := startConsole(dir, env, clamp(r.Cols, 80, 1000), clamp(r.Rows, 24, 1000))
	if errors.Is(err, errNoConsole) {
		release()
		return Terminal{}, errf("unsupported", "%v", err)
	}
	if err != nil {
		release()
		return Terminal{}, errf("internal", "%v", err)
	}
	t := &term{Terminal: Terminal{ID: termID(), Cwd: dir, Agent: r.Agent, Started: time.Now().UTC(), Running: true},
		con: con, more: make(chan struct{}), done: make(chan struct{}), closed: make(chan struct{}), in: make(chan []byte, 256)}
	go t.feed()
	n.terms.mu.Lock()
	n.terms.all[t.ID] = t
	n.terms.mu.Unlock()
	n.goLoop(func() {
		defer release()
		n.runTerm(t)
	})
	return t.view(), nil
}

func clamp(v, def, max int) int {
	if v <= 0 {
		return def
	}
	return min(v, max)
}

// runTerm reads the terminal's output while its shell runs, reaps the shell on its own (a
// process the shell left holding the terminal does not keep it), and then lets the terminal
// linger, unless it was closed.
func (n *Node) runTerm(t *term) {
	read := make(chan struct{})
	go func() {
		defer close(read)
		b := make([]byte, 32<<10)
		for {
			k, err := t.con.Read(b)
			if k > 0 {
				t.write(b[:k])
			}
			if err != nil {
				return
			}
		}
	}()
	waited := make(chan int, 1)
	go func() { waited <- t.con.Wait() }()
	defer t.con.Close() // closed for good when its reader is done (a pending read keeps the fd)
	var code int
	select {
	case code = <-waited:
	case <-n.ctx.Done(): // the node stops: the shell goes, then the rest
		t.kill()
		code = <-waited
	}
	// What is left to read, for a moment. A process the shell left may hold the terminal open
	// (the read goes on until it ends): the terminal ends all the same.
	select {
	case <-read:
	case <-time.After(3 * time.Second):
	}
	t.mu.Lock()
	t.Running, t.ExitCode = false, &code
	close(t.done)
	t.wake()
	t.mu.Unlock()
	select {
	case <-time.After(termLinger):
	case <-t.closed:
	case <-n.ctx.Done():
	}
	n.terms.mu.Lock()
	if n.terms.all[t.ID] == t {
		delete(n.terms.all, t.ID)
	}
	n.terms.mu.Unlock()
}

func (t *term) write(b []byte) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.Running {
		return // ended: its output is what it was then (a process left behind writes on in vain)
	}
	t.buf = append(t.buf, b...)
	if over := len(t.buf) - termKeep; over > 0 {
		t.buf = append([]byte(nil), t.buf[over:]...)
		t.base += int64(over)
	}
	t.Size += int64(len(b))
	t.wake()
}

func (t *term) wake() { // under t.mu
	close(t.more)
	t.more = make(chan struct{})
}

// kill ends the shell (and with it, as with any terminal, what it runs there).
func (t *term) kill() { t.con.Kill() }

func (t *term) view() Terminal {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.Terminal
}

func (n *Node) term(p Principal, id string) (*term, error) {
	if err := termAllowed(p); err != nil {
		return nil, err
	}
	n.terms.mu.Lock()
	defer n.terms.mu.Unlock()
	t := n.terms.all[id]
	if t == nil {
		return nil, errf("not_found", "no terminal %s", id)
	}
	return t, nil
}

// Terminals lists the open terminals.
func (n *Node) Terminals(p Principal) ([]Terminal, error) {
	if err := termAllowed(p); err != nil {
		return nil, err
	}
	n.terms.mu.Lock()
	all := make([]*term, 0, len(n.terms.all))
	for _, t := range n.terms.all {
		all = append(all, t)
	}
	n.terms.mu.Unlock()
	out := make([]Terminal, 0, len(all))
	for _, t := range all {
		out = append(out, t.view())
	}
	return out, nil
}

// TermInput types into a terminal.
func (n *Node) TermInput(p Principal, id string, in []byte) error {
	t, err := n.term(p, id)
	if err != nil {
		return err
	}
	if len(in) > termInputMax {
		return errf("invalid", "%d bytes of input at once, over %d", len(in), termInputMax)
	}
	select {
	case <-t.done:
		return errf("conflict", "the terminal's shell has ended")
	default:
	}
	// Queued for the shell: a program that does not read its input does not hold the request.
	t.mu.Lock()
	if t.queued+len(in) > termQueue {
		t.mu.Unlock()
		return errf("conflict", "the terminal does not take input (its program is not reading)")
	}
	t.queued += len(in)
	t.mu.Unlock()
	select {
	case t.in <- append([]byte(nil), in...):
		return nil
	default:
		t.mu.Lock()
		t.queued -= len(in)
		t.mu.Unlock()
		return errf("conflict", "the terminal does not take input (its program is not reading)")
	}
}

// feed writes the queued input to the shell, until the terminal ends.
func (t *term) feed() {
	for {
		select {
		case b := <-t.in:
			_, err := t.con.Write(b)
			t.mu.Lock()
			t.queued -= len(b)
			t.mu.Unlock()
			if err != nil {
				return
			}
		case <-t.done:
			return
		}
	}
}

// TermResize sets a terminal's size.
func (n *Node) TermResize(p Principal, id string, cols, rows int) error {
	t, err := n.term(p, id)
	if err != nil {
		return err
	}
	if cols <= 0 || rows <= 0 || cols > 1000 || rows > 1000 {
		return errf("invalid", "size %dx%d", cols, rows)
	}
	return t.con.Resize(cols, rows)
}

// CloseTerminal ends a terminal's shell and forgets it.
func (n *Node) CloseTerminal(p Principal, id string) error {
	t, err := n.term(p, id)
	if err != nil {
		return err
	}
	t.kill()
	t.once.Do(func() { close(t.closed) })
	n.terms.mu.Lock()
	delete(n.terms.all, id)
	n.terms.mu.Unlock()
	return nil
}

// TermOutput calls fn with the terminal's output from offset from (or the oldest kept, if that is
// gone: fn learns where it starts) as it comes, until the shell ends and all is read (then it
// returns the terminal) or ctx ends.
func (n *Node) TermOutput(ctx context.Context, p Principal, id string, from int64, fn func(offset int64, b []byte) error) (Terminal, error) {
	t, err := n.term(p, id)
	if err != nil {
		return Terminal{}, err
	}
	for {
		t.mu.Lock()
		if from < t.base {
			from = t.base
		}
		var chunk []byte
		if end := t.base + int64(len(t.buf)); from < end {
			chunk = append([]byte(nil), t.buf[from-t.base:]...)
		}
		more, ended, view := t.more, !t.Running, t.Terminal
		t.mu.Unlock()
		if len(chunk) > 0 {
			if err := fn(from, chunk); err != nil {
				return view, err
			}
			from += int64(len(chunk))
			continue
		}
		if ended {
			return view, nil
		}
		select {
		case <-more:
		case <-ctx.Done():
			return view, ctx.Err()
		}
	}
}

func termID() string {
	var b [12]byte
	_, _ = rand.Read(b[:])
	return "term_" + hex.EncodeToString(b[:])
}
