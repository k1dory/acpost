package node

// The acp driver: the node is an ACP client of each agent it runs. Each of an agent's
// `concurrency` slots is one agent process (started on the first message, stopped after
// idle_timeout) that takes the agent's messages one at a time. A message is a session/prompt in
// the session its key (task, thread or none) maps to; the turn's text is the reply.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/k1dory/acpx"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/store"
)

type acpAgent struct {
	n     *Node
	name  string
	addr  string
	cfg   *config.Agent
	slots []*acpSlot

	keyMu  sync.Mutex
	keys   map[string]*keyLock
	owners map[string]int // ACP session id → the slot whose process last ran it (concurrency > 1)

	optMu    sync.Mutex
	optAsk   sync.Mutex             // one asking the agent what it offers at a time
	lastOpts *acpx.SessionState     // the latest session's options: what the agent offers (models, levels, modes)
	commands []acp.AvailableCommand // the slash commands it said last
}

func (a *acpAgent) ownerOf(key string) int {
	a.keyMu.Lock()
	defer a.keyMu.Unlock()
	if o, ok := a.owners[key]; ok {
		return o
	}
	return -1
}

func (a *acpAgent) setOwner(key string, slot int) {
	a.keyMu.Lock()
	defer a.keyMu.Unlock()
	if len(a.owners) > 10000 {
		clear(a.owners) // a cache: losing it only costs a session/load
	}
	a.owners[key] = slot
}

type keyLock struct {
	mu   sync.Mutex
	refs int
}

type acpSlot struct {
	a     *acpAgent
	idx   int
	token string

	mu       sync.Mutex // guards proc
	proc     *acpProc
	lastUsed time.Time
}

type acpProc struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	conn     *acpx.Conn
	cl       *acpClient
	init     acp.InitializeResponse
	sessions map[string]acp.SessionId
	models   map[acp.SessionId]string            // the model each session runs, as the agent names it
	states   map[acp.SessionId]acpx.SessionState // each session's modes and configuration options
	// costBase is where a session's cost stood when this process took it up: 0 for one it created,
	// what the agent said while loading one (absent: not known).
	costBase map[acp.SessionId]float64
	done     chan struct{}
	exitErr  error
}

func newACPAgent(n *Node, name string, cfg *config.Agent) *acpAgent {
	a := &acpAgent{n: n, name: name, addr: "agent:" + name, cfg: cfg, keys: map[string]*keyLock{}, owners: map[string]int{}}
	for i := range cfg.Concurrency {
		a.slots = append(a.slots, &acpSlot{a: a, idx: i, token: n.issueAgentToken(name)})
	}
	return a
}

func (a *acpAgent) start() {
	for _, s := range a.slots {
		a.n.goLoop(s.loop)
	}
}

func (a *acpAgent) stop() {
	var wg sync.WaitGroup
	for _, s := range a.slots {
		wg.Add(1)
		go func() { defer wg.Done(); s.stopProc() }()
	}
	wg.Wait()
}

func (a *acpAgent) running() bool {
	for _, s := range a.slots {
		s.mu.Lock()
		up := s.proc != nil
		s.mu.Unlock()
		if up {
			return true
		}
	}
	return false
}

// lockKey serializes turns of one session key across slots.
func (a *acpAgent) lockKey(key string) func() {
	if key == "" {
		return func() {}
	}
	a.keyMu.Lock()
	k := a.keys[key]
	if k == nil {
		k = &keyLock{}
		a.keys[key] = k
	}
	k.refs++
	a.keyMu.Unlock()
	k.mu.Lock()
	return func() {
		k.mu.Unlock()
		a.keyMu.Lock()
		if k.refs--; k.refs == 0 {
			delete(a.keys, key)
		}
		a.keyMu.Unlock()
	}
}

// metaSession is the meta key of a message that continues an existing ACP session (send --session).
const (
	metaSession    = "acp_session"
	metaSessionCwd = "acp_session_cwd"
)

// lockID is what serializes a message's turns: the ACP session it runs in when that is known (a
// task's session and the same session named by id are one), else its session key.
func (s *acpSlot) lockID(m store.Message) string {
	key := sessionKey(s.a.cfg.Session, m)
	if id, ok := strings.CutPrefix(key, "session:"); ok {
		return "sid:" + id
	}
	if key != "" {
		if sid, _ := s.a.n.st.Session(s.a.n.ctx, s.a.addr, key); sid != "" {
			return "sid:" + sid
		}
	}
	return key
}

func sessionKey(mode string, m store.Message) string {
	if id := m.Meta[metaSession]; id != "" {
		return "session:" + id
	}
	switch mode {
	case "message":
		return ""
	case "thread":
		return "thread:" + m.Thread
	}
	if m.Task != "" {
		return "task:" + m.Task
	}
	return "thread:" + m.Thread
}

func (s *acpSlot) loop() {
	n, a := s.a.n, s.a
	defer s.stopProc()
	for n.ctx.Err() == nil {
		m, ok, err := n.claim(n.ctx, []string{a.addr}, min(time.Second, max(a.cfg.IdleTimeout.D()/2, 10*time.Millisecond)), a.cfg.Lease.D())
		if err != nil {
			if n.ctx.Err() == nil {
				n.log.Warn("claim", "agent", a.name, "err", err)
				time.Sleep(time.Second)
			}
			continue
		}
		if !ok {
			s.mu.Lock()
			idle := s.proc != nil && time.Since(s.lastUsed) > a.cfg.IdleTimeout.D()
			s.mu.Unlock()
			if idle {
				n.log.Info("agent idle: stopping its process", "agent", a.name, "slot", s.idx)
				s.stopProc()
			}
			continue
		}
		s.handle(m)
		s.mu.Lock()
		s.lastUsed = time.Now()
		s.mu.Unlock()
	}
}

func (s *acpSlot) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "ACPOST_") {
			env = append(env, kv)
		}
	}
	for k, v := range s.a.cfg.Env {
		env = append(env, k+"="+v)
	}
	return append(env, "ACPOST_NODE="+s.a.n.LocalURL(), "ACPOST_TOKEN="+s.token)
}

func (s *acpSlot) cwd() string {
	if s.a.cfg.Cwd != "" {
		return s.a.cfg.Cwd
	}
	home, _ := os.UserHomeDir()
	return home
}

// ensureProc returns the slot's agent process, starting and initializing it if needed.
func (s *acpSlot) ensureProc(ctx context.Context) (*acpProc, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.proc; p != nil {
		select {
		case <-p.done:
			// The leader is gone; what it started may not be.
			s.proc = nil
			killGroupNow(p)
		default:
			return p, nil
		}
	}
	a, n := s.a, s.a.n
	cmd := exec.Command(a.cfg.Command[0], a.cfg.Command[1:]...)
	cmd.Dir = s.cwd()
	cmd.Env = s.env()
	// The agent and whatever it starts are one process group, stopped as a whole.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", a.cfg.Command[0], err)
	}
	p := &acpProc{cmd: cmd, stdin: stdin, sessions: map[string]acp.SessionId{}, models: map[acp.SessionId]string{}, states: map[acp.SessionId]acpx.SessionState{}, costBase: map[acp.SessionId]float64{}, done: make(chan struct{})}
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			n.log.Debug("agent stderr", "agent", a.name, "line", sc.Text())
		}
		_, _ = io.Copy(io.Discard, stderr)
	}()
	go func() { p.exitErr = cmd.Wait(); close(p.done) }()

	wire := acpx.NewWire(stdin, stdout, acpx.WireOptions{Notices: true})
	p.cl = &acpClient{slot: s, wire: wire, turns: map[acp.SessionId]*acpTurn{}, loadedCost: map[acp.SessionId]acp.Cost{}}
	p.conn = wire.Connect(p.cl)
	p.conn.SetLogger(n.log.With("agent", a.name))
	ictx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	fail := func(err error) (*acpProc, error) {
		killGroup(p)
		return nil, err
	}
	init, err := p.conn.Initialize(ictx, acp.InitializeRequest{
		ProtocolVersion: acp.ProtocolVersionNumber,
		// The agent works with its own tools: the node offers no file system or terminal.
		// terminal_output: codex-acp then sends a command's output and exit code with its tool call.
		ClientCapabilities: acp.ClientCapabilities{Meta: map[string]any{"terminal_output": true}},
		ClientInfo:         &acp.Implementation{Name: "acpost", Version: Version},
	})
	if err != nil {
		return fail(fmt.Errorf("initialize: %w", procErr(p, err)))
	}
	p.init = init
	if m := a.cfg.AuthMethod; m != "" {
		if _, err := p.conn.Authenticate(ictx, acp.AuthenticateRequest{MethodId: m}); err != nil {
			return fail(fmt.Errorf("authenticate %s: %w", m, procErr(p, err)))
		}
	}
	n.log.Info("agent started", "agent", a.name, "slot", s.idx, "pid", cmd.Process.Pid, "impl", implName(init))
	s.proc, s.lastUsed = p, time.Now()
	return p, nil
}

func implName(init acp.InitializeResponse) string {
	if init.AgentInfo != nil {
		return init.AgentInfo.Name + " " + init.AgentInfo.Version
	}
	return ""
}

// procErr explains an ACP error by the process's exit when it died.
func procErr(p *acpProc, err error) error {
	select {
	case <-p.done:
		return fmt.Errorf("the agent exited (%v)", p.exitErr)
	case <-time.After(500 * time.Millisecond):
		return err
	}
}

func (s *acpSlot) dropProc(p *acpProc) {
	s.mu.Lock()
	if s.proc == p {
		s.proc = nil
	}
	s.mu.Unlock()
	killGroup(p)
}

func (s *acpSlot) stopProc() {
	s.mu.Lock()
	p := s.proc
	s.proc = nil
	s.mu.Unlock()
	if p == nil {
		return
	}
	// End of input first: a well-behaved agent exits on its own.
	_ = p.stdin.Close()
	select {
	case <-p.done:
	case <-time.After(3 * time.Second):
	}
	killGroup(p)
}

// killGroupNow kills an agent's process group at once (SIGKILL: also a stopped or stuck one).
func killGroupNow(p *acpProc) {
	if p.cmd.Process == nil {
		return
	}
	_ = p.stdin.Close()
	_ = syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	<-p.done
}

// killGroup stops what is left of an agent's process group: SIGTERM, then SIGKILL.
func killGroup(p *acpProc) {
	if p.cmd.Process == nil {
		return
	}
	_ = p.stdin.Close()
	pgid := p.cmd.Process.Pid
	if syscall.Kill(-pgid, syscall.SIGTERM) != nil {
		return
	}
	select {
	case <-p.done:
	case <-time.After(2 * time.Second):
	}
	_ = syscall.Kill(-pgid, syscall.SIGKILL)
	<-p.done
}

// session returns the ACP session for a key: one this process has, one stored (resumed or
// loaded), or a new one.
func (s *acpSlot) session(ctx context.Context, p *acpProc, key string) (acp.SessionId, error) {
	return s.sessionIn(ctx, p, key, "")
}

// sessionIn is session with the working directory of a session the sender named (when known).
func (s *acpSlot) sessionIn(ctx context.Context, p *acpProc, key, sessionCwd string) (acp.SessionId, error) {
	a, n := s.a, s.a.n
	if key != "" {
		if sid, ok := p.sessions[key]; ok {
			if a.ownerOf(string(sid)) == s.idx {
				// Retention measures from the last use.
				_ = n.st.PutSession(ctx, a.addr, key, string(sid))
				return sid, nil
			}
			// Another slot's process advanced this session since: load it again here.
			delete(p.sessions, key)
		}
	}
	cwd := s.cwd()
	if sessionCwd != "" {
		cwd = sessionCwd // the session's own directory, or the one the task was started in
	}
	if id, ok := strings.CutPrefix(key, "session:"); ok {
		// A session named by the sender: continue exactly it, or fail. Its mode and model are the
		// session's own, not the agent's configuration.
		sid, err := s.continueSession(ctx, p, acp.SessionId(id), cwd)
		if err != nil {
			return "", explainACP(procErr(p, err), "session "+id, true)
		}
		p.sessions[key] = sid
		a.setOwner(string(sid), s.idx)
		return sid, nil
	}
	var sid acp.SessionId
	var st acpx.SessionState
	if key != "" {
		if prev, _ := n.st.Session(ctx, a.addr, key); prev != "" {
			caps := p.init.AgentCapabilities
			if caps.SessionCapabilities.Resume != nil {
				if r, err := p.conn.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: acp.SessionId(prev), Cwd: cwd, McpServers: []acp.McpServer{}}); err == nil {
					sid, st = acp.SessionId(prev), acpx.SessionState{Modes: r.Modes, Options: r.ConfigOptions}
				}
			}
			if sid == "" && caps.LoadSession {
				// The history the agent replays is not attributed to any turn: no turn is registered yet.
				if r, err := p.conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: acp.SessionId(prev), Cwd: cwd, McpServers: []acp.McpServer{}}); err == nil {
					sid, st = acp.SessionId(prev), acpx.SessionState{Modes: r.Modes, Options: r.ConfigOptions}
				}
			}
		}
	}
	if sid == "" {
		r, err := p.conn.NewSession(ctx, acp.NewSessionRequest{Cwd: cwd, McpServers: []acp.McpServer{}})
		if err != nil {
			return "", fmt.Errorf("session/new: %w", procErr(p, err))
		}
		sid, st = r.SessionId, acpx.SessionState{Modes: r.Modes, Options: r.ConfigOptions}
		p.costBase[sid] = 0 // a new session: its cost starts here
	}
	for _, set := range []struct {
		s    acpx.Setting
		want string
	}{{acpx.ModeSetting, a.cfg.Mode}, {acpx.ModelSetting, a.cfg.Model}, {acpx.ThoughtLevelSetting, a.cfg.ThoughtLevel}} {
		next, err := acpx.Configure(ctx, p.conn, sid, st, set.s, set.want)
		if err != nil {
			n.log.Warn("configure session", "agent", a.name, "setting", set.s.Name, "err", err)
			continue
		}
		st = next
	}
	a.noteState(p, sid, st)
	if key != "" {
		p.sessions[key] = sid
		a.setOwner(string(sid), s.idx)
		if err := n.st.PutSession(ctx, a.addr, key, string(sid)); err != nil {
			n.log.Warn("store session", "agent", a.name, "err", err)
		}
	}
	return sid, nil
}

// explainStart says why an agent process did not start.
func explainStart(err error) Failure {
	var re *acp.RequestError
	if errors.As(err, &re) || strings.Contains(err.Error(), "authenticate") {
		f := explainACP(err, "start the agent", false)
		if f.Code == "agent_auth" {
			return f
		}
	}
	return failf("agent_start", "the agent did not start: %v", err)
}

// continueSession resumes a session (session/resume) or loads it (session/load), whichever the
// agent offers.
func (s *acpSlot) continueSession(ctx context.Context, p *acpProc, id acp.SessionId, cwd string) (acp.SessionId, error) {
	caps := p.init.AgentCapabilities
	var errs []error
	if caps.SessionCapabilities.Resume != nil {
		r, err := p.conn.ResumeSession(ctx, acp.ResumeSessionRequest{SessionId: id, Cwd: cwd, McpServers: []acp.McpServer{}})
		if err == nil {
			s.a.noteState(p, id, acpx.SessionState{Modes: r.Modes, Options: r.ConfigOptions})
			return id, nil
		}
		errs = append(errs, err)
	}
	if caps.LoadSession {
		r, err := p.conn.LoadSession(ctx, acp.LoadSessionRequest{SessionId: id, Cwd: cwd, McpServers: []acp.McpServer{}})
		if err == nil {
			s.a.noteState(p, id, acpx.SessionState{Modes: r.Modes, Options: r.ConfigOptions})
			return id, nil
		}
		errs = append(errs, err)
	}
	if len(errs) == 0 {
		return "", errors.New("it offers neither session/resume nor session/load")
	}
	// resume and load usually fail for the same reason: say it once.
	if len(errs) == 2 && errs[0].Error() == errs[1].Error() {
		return "", errs[0]
	}
	return "", errors.Join(errs...)
}

func (s *acpSlot) prompt(m store.Message, files []attached) string {
	var b strings.Builder
	if len(files) > 0 {
		b.WriteString("[acpost] Attached files:\n")
		for _, f := range files {
			switch {
			case f.Link():
				fmt.Fprintf(&b, "- %s (%s): a link, %s\n", f.Name, f.Type, f.URI)
			case f.embedded:
				fmt.Fprintf(&b, "- %s (%s, %d bytes): in this prompt\n", f.Name, f.Type, f.Size)
			default:
				fmt.Fprintf(&b, "- %s (%s, %d bytes): %s (a copy until this turn ends)\n", f.Name, f.Type, f.Size, f.path)
			}
		}
		b.WriteString("\n")
	}
	if !s.a.cfg.PreambleOn() {
		b.WriteString(m.Body)
		return b.String()
	}
	fmt.Fprintf(&b, "[acpost] Message %s from %s", m.ID, m.From)
	if m.Task != "" {
		fmt.Fprintf(&b, ", task %s", m.Task)
	}
	if m.Attempts > 1 {
		fmt.Fprintf(&b, " (attempt %d: an earlier attempt was interrupted)", m.Attempts)
	}
	b.WriteString(".\nYour final answer in this turn is sent back to the sender as the reply. " +
		"To ask another agent, run `acpost send --to agent:<name> --body \"…\" --wait`. " +
		"To attach files to your reply, run `acpost attach FILE…`.\n\n")
	b.WriteString(m.Body)
	return b.String()
}

// handle runs one message as one turn.
func (s *acpSlot) handle(m store.Message) {
	a, n := s.a, s.a.n
	unlock := a.lockKey(s.lockID(m))
	defer unlock()
	n.setCurrent(s.token, m.ID, m.LeaseID)
	defer n.setCurrent(s.token, "", "")

	// The claim may have waited for the session key's lock longer than its lease: if it was reaped
	// (and maybe taken again) meanwhile, it is not ours to run.
	if err := n.st.Extend(n.ctx, m.ID, m.LeaseID, a.cfg.Lease.D()); err != nil {
		return
	}
	tl := n.watchTurn(m, a.cfg.Lease.D())
	defer tl.stop()
	ctx, cancelled, lost := tl.ctx, &tl.cancelled, &tl.lost

	var turn *acpTurn
	var tf *turnFiles
	complete := func(state store.State, reply string, f Failure) {
		max := int(n.cfg.Limits.MaxReply)
		if turn != nil {
			// What the agent handed back as files goes with its reply, under this attempt's lease.
			got, dropped := turn.blocks()
			said := n.keepReplyFiles(n.ctx, m, got, dropped, tf)
			// Events stay on this node: a sender elsewhere learns what was left out from the reply,
			// at its end, kept when the agent's text is cut to max_reply (up to half of it).
			if len(said) > 0 && n.senderElsewhere(m) {
				note := capReply("\n\n[acpost] "+strings.Join(said, "\n[acpost] "), max/2)
				reply = capReply(reply, max-len(note)) + note
			}
		}
		reply = capReply(reply, max)
		if err := n.st.Complete(n.ctx, m.ID, m.LeaseID, state, reply, f.Code, f.Message); err != nil && !errors.Is(err, store.ErrLeaseLost) {
			n.log.Warn("complete", "id", m.ID, "err", err)
		}
		n.notify()
	}
	release := func(f Failure) {
		if _, err := n.st.Release(n.ctx, m.ID, m.LeaseID, f.Code, f.Message, n.cfg.Limits.MaxAttempts); err != nil && !errors.Is(err, store.ErrLeaseLost) {
			n.log.Warn("release", "id", m.ID, "err", err)
		}
		n.notify()
	}

	p, err := s.ensureProc(ctx)
	if err != nil {
		if n.ctx.Err() != nil || cancelled.Load() {
			return
		}
		n.log.Warn("agent did not start", "agent", a.name, "err", err)
		release(explainStart(err))
		time.Sleep(time.Second) // do not spin on a broken command
		return
	}
	key := sessionKey(a.cfg.Session, m)
	cwd := taskCwd(m)
	if cwd == "" && m.Meta[metaSession] != "" {
		// A session named without its directory (through a hub): the agent's own list says it.
		cwd = n.sessionCwd(ctx, a.name, m.Meta[metaSession])
		if m.Meta == nil {
			m.Meta = map[string]string{}
		}
		m.Meta[metaSessionCwd] = cwd
	}
	sid, err := s.sessionIn(ctx, p, key, cwd)
	if err != nil {
		if n.ctx.Err() != nil || cancelled.Load() {
			return
		}
		select {
		case <-p.done:
			s.dropProc(p)
			release(failf("agent_crashed", "the agent exited (%v)", p.exitErr))
		default:
			complete(store.Failed, "", explainACP(err, "open the session", strings.HasPrefix(key, "session:")))
		}
		return
	}
	s.applySettings(ctx, p, sid, m)
	blocks, tfiles, cleanup, err := s.promptBlocks(ctx, m, p.init.AgentCapabilities.PromptCapabilities)
	tf = tfiles
	defer cleanup()
	if err != nil {
		if n.ctx.Err() == nil && !cancelled.Load() {
			release(failf("agent_error", "the message's files: %v", err))
		}
		return
	}
	// The working tree before the turn: what the turn changed is told after it (changes.go).
	dir := s.cwd()
	if c := taskCwd(m); c != "" {
		dir = c
	}
	before := snapshot(ctx, dir)
	turn = p.cl.begin(sid, m.ID, int(n.cfg.Limits.MaxReply), n.cfg.Limits.MaxFiles, int(n.cfg.Limits.MaxFilesSize))
	turnCtx, turnCancel := context.WithTimeout(ctx, a.cfg.TurnTimeout.D())
	turn.mu.Lock()
	turn.ctx = turnCtx // what waits for a person ends with the turn
	turn.mu.Unlock()
	// The SDK writes a request before it watches the context: an agent that stopped reading its
	// stdin holds Prompt in that write. If Prompt has not returned shortly after the turn ended,
	// the process is killed, which ends the write.
	promptDone := make(chan struct{})
	var stuck atomic.Bool
	stopWatch := context.AfterFunc(turnCtx, func() {
		select {
		case <-promptDone:
		case <-time.After(300 * time.Millisecond):
			stuck.Store(true)
			s.mu.Lock()
			if s.proc == p {
				s.proc = nil
			}
			s.mu.Unlock()
			killGroupNow(p)
		}
	})
	resp, err := p.conn.Prompt(turnCtx, acp.PromptRequest{SessionId: sid, Prompt: blocks})
	close(promptDone)
	s.recordUsage(m.ID, p, sid, turn, resp.Usage) // whatever the outcome: a failed turn may have cost too
	stopWatch()
	cleanup() // the copies go with the turn, before anyone learns it ended
	timedOut := errors.Is(turnCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil
	turnCancel()
	p.cl.end(sid)
	n.recordChanges(m, a.addr+"@"+n.Name(), before, dir) // after the turn's deadline was read
	reply := turn.reply(a.cfg.Reply)

	if err != nil || timedOut {
		if n.ctx.Err() != nil || lost.Load() {
			return // the node is stopping (the message is requeued at the next start), or the lease is gone
		}
		if stuck.Load() {
			if timedOut {
				complete(store.Failed, reply, failf("turn_timeout", "the turn ran past %s (turn_timeout); the agent stopped reading and was killed", a.cfg.TurnTimeout.D()))
			}
			return
		}
		// Tell the agent the turn is over.
		cctx, done := context.WithTimeout(context.Background(), 2*time.Second)
		_ = p.conn.Cancel(cctx, acp.CancelNotification{SessionId: sid})
		done()
		select {
		case <-p.done:
			s.dropProc(p)
			if !cancelled.Load() {
				release(failf("agent_crashed", "the agent exited mid-turn (%v)", p.exitErr))
			}
			return
		default:
		}
		switch {
		case cancelled.Load():
		case timedOut:
			complete(store.Failed, reply, failf("turn_timeout", "the turn ran past %s (turn_timeout)", a.cfg.TurnTimeout.D()))
		default:
			if pe := procErr(p, err); pe != err {
				s.dropProc(p)
				release(failf("agent_crashed", "%v", pe))
				return
			}
			complete(store.Failed, reply, explainACP(err, "the turn", false))
		}
		s.closeIfOneOff(p, key, sid)
		return
	}
	switch resp.StopReason {
	case acp.StopReasonEndTurn:
		complete(store.Done, reply, Failure{})
	case acp.StopReasonCancelled:
		if !cancelled.Load() {
			complete(store.Failed, reply, failf("agent_stopped", "the agent cancelled the turn"))
		}
	default:
		complete(store.Failed, reply, failf("agent_stopped", "the agent stopped the turn: %s", resp.StopReason))
	}
	s.closeIfOneOff(p, key, sid)
}

// closeIfOneOff closes a session no later message will use (session: message).
func (s *acpSlot) closeIfOneOff(p *acpProc, key string, sid acp.SessionId) {
	if key != "" || p.init.AgentCapabilities.SessionCapabilities.Close == nil {
		return
	}
	ctx, done := context.WithTimeout(s.a.n.ctx, 5*time.Second)
	defer done()
	_, _ = p.conn.CloseSession(ctx, acp.CloseSessionRequest{SessionId: sid})
}

// acpTurn collects what the agent says in one turn.
type acpTurn struct {
	msgID  string
	limit  int
	stream *textStream // its events, the text as it comes among them
	mu     sync.Mutex
	all    strings.Builder
	last   strings.Builder
	// Files the agent handed back, within the reply's limits (more are counted, not kept).
	files              []replyBlock
	maxFiles, maxBytes int
	bytes, meta        int // content, and names, links and types (each bounded on its own)
	dropped            int
	usage              *acp.SessionUsageUpdate // the agent's last word on the session's context and cost
	tools              map[string]*toolTrack   // the turn's tool calls as their updates made them
	ctx                context.Context         // the turn's: ended by its timeout or its message's cancel
	history            bool                    // a session's history replayed (session/load): its user's messages too
}

func (t *acpTurn) file(b replyBlock) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Names, links and types are bounded as they come, and count with the content: what is kept
	// while the turn runs stays within the reply's limits.
	if len(b.uri) > 2048 || len(b.name) > 255 || len(b.typ) > 127 {
		t.dropped++
		return
	}
	if len(t.files) >= t.maxFiles || t.bytes+len(b.data) > t.maxBytes {
		t.dropped++
		return
	}
	t.files = append(t.files, b)
	t.bytes += len(b.data)
	t.meta += len(b.uri) + len(b.name) + len(b.typ) // at most max_files × 2430 bytes, by the checks above
}

// blocks are the files the agent handed back, and how many more there were than the limits keep.
func (t *acpTurn) blocks() ([]replyBlock, int) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.files, t.dropped
}

func (t *acpTurn) text(s string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// Keep a little more than the limit, so the cut is visible; never grow without bound.
	if t.all.Len() <= t.limit {
		t.all.WriteString(s)
	}
	if t.last.Len() <= t.limit {
		t.last.WriteString(s)
	}
}

func (t *acpTurn) toolCall() {
	t.mu.Lock()
	t.last.Reset()
	t.mu.Unlock()
}

func (t *acpTurn) reply(mode string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if mode == "last" && strings.TrimSpace(t.last.String()) != "" {
		return t.last.String()
	}
	return t.all.String()
}

// acpClient is the client side of one agent process.
type acpClient struct {
	slot  *acpSlot
	wire  *acpx.Wire
	mu    sync.Mutex
	turns map[acp.SessionId]*acpTurn
	// loadedCost is a session's cost as the agent said it outside a turn (loading the session).
	loadedCost map[acp.SessionId]acp.Cost
}

// base is where a session's cost in currency stood before its first turn here, when known: 0 for
// a session created here, what the agent said loading it in that currency.
func (c *acpClient) base(sid acp.SessionId, created map[acp.SessionId]float64, currency string) (float64, bool) {
	if b, ok := created[sid]; ok {
		return b, true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	b, ok := c.loadedCost[sid]
	return b.Amount, ok && strings.EqualFold(b.Currency, currency)
}

func (c *acpClient) begin(sid acp.SessionId, msgID string, limit, maxFiles, maxBytes int) *acpTurn {
	t := &acpTurn{msgID: msgID, limit: limit, maxFiles: maxFiles, maxBytes: maxBytes}
	// Half of what the message's events have left is for this turn's text (earlier attempts took theirs).
	n := c.slot.a.n
	had, _ := n.st.EventCount(n.ctx, msgID)
	t.stream = &textStream{record: func(kind string, data any) { c.record(t, kind, data) }, budget: max(n.cfg.Limits.MaxEventsPerMessage-had, 0) / 2}
	c.mu.Lock()
	c.turns[sid] = t
	c.mu.Unlock()
	return t
}

func (c *acpClient) end(sid acp.SessionId) {
	if t := c.turn(sid); t != nil {
		t.stream.close() // the turn's text is all in its events before anyone learns it ended
	}
	c.mu.Lock()
	delete(c.turns, sid)
	c.mu.Unlock()
}

func (c *acpClient) turn(sid acp.SessionId) *acpTurn {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.turns[sid]
}

// event records an event of the turn, after the text that came before it.
func (c *acpClient) event(t *acpTurn, kind string, data any) { t.stream.event(kind, data) }

func (c *acpClient) record(t *acpTurn, kind string, data any) {
	b := eventJSON(data)
	n := c.slot.a.n
	if err := n.addEvent(n.ctx, t.msgID, kind, b, true); err != nil && n.ctx.Err() == nil {
		n.log.Debug("event", "err", err)
	}
}

func (c *acpClient) SessionUpdate(ctx context.Context, p acp.SessionNotification) error {
	extra, sdk := c.wire.Classify(p.Update)
	if u := p.Update.AvailableCommandsUpdate; sdk && u != nil {
		c.slot.a.noteCommands(u.AvailableCommands) // whenever said: after session/new, in a turn
	}
	t := c.turn(p.SessionId)
	if t == nil {
		// A replayed history (session/load) or a late update: only where the session's cost stands.
		if uu := p.Update.UsageUpdate; sdk && uu != nil && uu.Cost != nil {
			c.mu.Lock()
			c.loadedCost[p.SessionId] = *uu.Cost
			c.mu.Unlock()
		}
		return nil
	}
	if !sdk {
		if extra != nil && extra.Notice != nil {
			c.event(t, "notice", extra.Notice)
		}
		return nil
	}
	u := p.Update
	switch {
	case u.UserMessageChunk != nil && t.history:
		if tb := u.UserMessageChunk.Content.Text; tb != nil {
			t.stream.add("user", tb.Text)
		}
	case u.AgentMessageChunk != nil:
		if tb := u.AgentMessageChunk.Content.Text; tb != nil {
			t.text(tb.Text)
			t.stream.add("text", tb.Text)
		} else if b, ok := fileBlock(u.AgentMessageChunk.Content); ok {
			// A file in the answer: kept with the reply, not written into its text.
			t.file(b)
		}
	case u.AgentThoughtChunk != nil:
		if tb := u.AgentThoughtChunk.Content.Text; tb != nil {
			t.stream.add("thought", tb.Text)
		}
	case u.ToolCall != nil:
		t.toolCall()
		c.event(t, "tool_call", t.trackCall(u.ToolCall))
	case u.ToolCallUpdate != nil:
		// The call as its updates made it, when there is something to say (not every tick).
		if e, ok := t.trackUpdate(u.ToolCallUpdate); ok {
			c.event(t, "tool_call_update", e)
		}
	case u.UsageUpdate != nil:
		t.mu.Lock()
		t.usage = u.UsageUpdate
		t.mu.Unlock()
	case u.Plan != nil:
		var entries []string
		for _, e := range u.Plan.Entries {
			entries = append(entries, string(e.Status)+": "+e.Content)
		}
		c.event(t, "plan", entries)
	}
	return nil
}

func (c *acpClient) RequestPermission(ctx context.Context, p acp.RequestPermissionRequest) (acp.RequestPermissionResponse, error) {
	if c.slot.a.cfg.Permissions == "ask" {
		if t := c.turn(p.SessionId); t != nil {
			return c.ask(ctx, t, p)
		}
	}
	allow := c.slot.a.cfg.Permissions == "allow"
	want := []acp.PermissionOptionKind{acp.PermissionOptionKindRejectOnce, acp.PermissionOptionKindRejectAlways}
	if allow {
		want = []acp.PermissionOptionKind{acp.PermissionOptionKindAllowOnce, acp.PermissionOptionKindAllowAlways}
	}
	var chosen *acp.PermissionOption
	for _, k := range want {
		for i := range p.Options {
			if p.Options[i].Kind == k {
				chosen = &p.Options[i]
				break
			}
		}
		if chosen != nil {
			break
		}
	}
	title := ""
	if p.ToolCall.Title != nil {
		title = *p.ToolCall.Title
	}
	decision := "cancelled (no matching option)"
	out := acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeCancelled()}
	if chosen != nil {
		decision = string(chosen.Kind)
		out = acp.RequestPermissionResponse{Outcome: acp.NewRequestPermissionOutcomeSelected(chosen.OptionId)}
	}
	if t := c.turn(p.SessionId); t != nil {
		c.event(t, "permission", map[string]any{"tool": title, "id": p.ToolCall.ToolCallId, "decision": decision})
	}
	return out, nil
}

var errNotOffered = acp.NewMethodNotFound("not offered by acpost: the agent uses its own tools")

func (c *acpClient) ReadTextFile(context.Context, acp.ReadTextFileRequest) (acp.ReadTextFileResponse, error) {
	return acp.ReadTextFileResponse{}, errNotOffered
}

func (c *acpClient) WriteTextFile(context.Context, acp.WriteTextFileRequest) (acp.WriteTextFileResponse, error) {
	return acp.WriteTextFileResponse{}, errNotOffered
}

func (c *acpClient) CreateTerminal(context.Context, acp.CreateTerminalRequest) (acp.CreateTerminalResponse, error) {
	return acp.CreateTerminalResponse{}, errNotOffered
}

func (c *acpClient) KillTerminal(context.Context, acp.KillTerminalRequest) (acp.KillTerminalResponse, error) {
	return acp.KillTerminalResponse{}, errNotOffered
}

func (c *acpClient) TerminalOutput(context.Context, acp.TerminalOutputRequest) (acp.TerminalOutputResponse, error) {
	return acp.TerminalOutputResponse{}, errNotOffered
}

func (c *acpClient) ReleaseTerminal(context.Context, acp.ReleaseTerminalRequest) (acp.ReleaseTerminalResponse, error) {
	return acp.ReleaseTerminalResponse{}, errNotOffered
}

func (c *acpClient) WaitForTerminalExit(context.Context, acp.WaitForTerminalExitRequest) (acp.WaitForTerminalExitResponse, error) {
	return acp.WaitForTerminalExitResponse{}, errNotOffered
}

// Sessions lists an acp agent's sessions (ACP session/list), newest first.
func (n *Node) Sessions(ctx context.Context, p Principal, agent, cwd, cursor string) ([]client.SessionInfo, string, error) {
	cfg := n.agentCfg(agent)
	if cfg == nil {
		return nil, "", errh("not_found", "`acpost agents` lists the agents this node knows", "no agent %s here", agent)
	}
	if cfg.Driver != "acp" {
		return nil, "", errh("unsupported", "only acp agents have sessions", "agent:%s is an %s mailbox: it has no ACP sessions", agent, cfg.Driver)
	}
	if err := canUseSessions(p, agent, cfg); err != nil {
		return nil, "", err
	}
	slot := n.acp[agent].slots[0]
	proc, err := slot.ensureProc(ctx)
	if err != nil {
		f := explainStart(err)
		return nil, "", errh(f.Code, HintFor(f.Code), "%s", f.Message)
	}
	if proc.init.AgentCapabilities.SessionCapabilities.List == nil {
		return nil, "", errh("unsupported", HintFor("unsupported"), "agent:%s does not list its sessions (ACP session/list)", agent)
	}
	req := acp.ListSessionsRequest{}
	if cwd != "" {
		req.Cwd = &cwd
	}
	if cursor != "" {
		req.Cursor = &cursor
	}
	resp, err := proc.conn.ListSessions(ctx, req)
	if err != nil {
		f := explainACP(err, "list sessions", false)
		return nil, "", errh(f.Code, HintFor(f.Code), "%s", f.Message)
	}
	out := make([]client.SessionInfo, 0, len(resp.Sessions))
	for _, si := range resp.Sessions {
		info := client.SessionInfo{ID: string(si.SessionId), Cwd: si.Cwd}
		if si.Title != nil {
			info.Title = *si.Title
		}
		if si.UpdatedAt != nil {
			info.UpdatedAt = *si.UpdatedAt
		}
		out = append(out, info)
	}
	sort.SliceStable(out, func(i, j int) bool { return newer(out[i].UpdatedAt, out[j].UpdatedAt) })
	next := ""
	if resp.NextCursor != nil {
		next = *resp.NextCursor
	}
	return out, next, nil
}

// newer compares two RFC 3339 times (an unparsable one is oldest).
func newer(a, b string) bool {
	ta, ea := time.Parse(time.RFC3339Nano, a)
	tb, eb := time.Parse(time.RFC3339Nano, b)
	if ea != nil || eb != nil {
		return ea == nil
	}
	return ta.After(tb)
}

// setModel notes the model a session runs, from its configuration options.
func (p *acpProc) setModel(sid acp.SessionId, options []acp.SessionConfigOption) {
	if opt := acpx.ConfigOption(options, acp.SessionConfigOptionCategoryModel); opt != nil {
		p.models[sid] = modelName(opt)
	}
}

// recordUsage records what a turn used and cost, as the agent reported it (its tokens in the
// prompt response, an unstable part of ACP; the session's cumulative cost in usage_update), when it
// reported anything. The cost is the turn's own: how much the session's cost grew since its turn
// before, kept here where the agent runs; the event, with the turn's time and an id of its own,
// is counted once on each node that gets it (the hub's, the sender's).
func (s *acpSlot) recordUsage(msgID string, p *acpProc, sid acp.SessionId, t *acpTurn, u *acp.Usage) {
	t.mu.Lock()
	uu := t.usage
	t.mu.Unlock()
	if u == nil && uu == nil {
		return
	}
	n := s.a.n
	model := p.models[sid]
	if model == "" {
		model = s.a.cfg.Model
	}
	out := store.Usage{Turn: store.NewID(), At: time.Now().UTC(), Agent: s.a.addr + "@" + n.Name(), Session: string(sid), Model: model}
	if u != nil {
		out.Reported = true
		out.Input, out.Output, out.Total = u.InputTokens, u.OutputTokens, u.TotalTokens
		out.CacheRead, out.CacheWrite, out.Thought = deref(u.CachedReadTokens), deref(u.CachedWriteTokens), deref(u.ThoughtTokens)
	}
	if uu != nil {
		out.ContextUsed, out.ContextSize = uu.Used, uu.Size
		if uu.Cost != nil {
			var base *float64
			if b, ok := p.cl.base(sid, p.costBase, uu.Cost.Currency); ok {
				base = &b
			}
			cost, known, err := n.st.TurnCost(n.ctx, out.Agent, out.Session, uu.Cost.Currency, uu.Cost.Amount, base)
			if err == nil && known {
				out.Cost, out.Currency = &cost, uu.Cost.Currency
			}
		}
	}
	// Kept even when the message is final already (cancelled while the turn ran).
	if err := n.addEvent(n.ctx, msgID, "usage", eventJSON(out), false); err != nil && n.ctx.Err() == nil {
		n.log.Debug("usage", "err", err)
	}
}

func deref(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// modelName names the model a session runs for usage, so one model has one name (prices go by it):
// for a "Default…" option its description (claude-agent-acp: "default" is "Opus 5.5"); a value
// that is a model id (it has a digit: gpt-6.1-sol, claude-haiku-4-5-20251001) as it is; an alias
// by the option's name (claude-agent-acp: "opus" is "Opus 5.5").
func modelName(opt *acp.SessionConfigOptionSelect) string {
	value := string(opt.CurrentValue)
	for _, o := range acpx.SelectOptions(opt) {
		if o.Value != opt.CurrentValue {
			continue
		}
		desc := ""
		if o.Description != nil {
			desc = strings.TrimSpace(*o.Description)
		}
		switch name := strings.TrimSpace(o.Name); {
		case strings.HasPrefix(strings.ToLower(name), "default") || value == "default":
			if desc != "" {
				return desc
			}
		case strings.ContainsAny(value, "0123456789"):
			return value
		case name != "":
			return name
		}
	}
	return value
}
