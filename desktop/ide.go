//go:build darwin

package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/k1dory/acpost/desktop/preview"
)

// The IDE: VS Code's own web server (code serve-web, the VS Code installed on this Mac) on
// 127.0.0.1, behind a token of this run, shown in a native pane of the window. Only VS Code's
// engine is the Mac's: the server, settings and extensions are the app's own (ideDataDir), and
// it is dressed in the app's colours and fonts (ideTheme).

// ideBudget is the whole of one start: VS Code's server fetched (the first time), started and
// serving, and the app's look installed. IDEStart waits as long, no longer: a start that has not
// served by then is ended, not left to succeed after the interface was told it failed.
const ideBudget = 4 * time.Minute

// idePort is the IDE's address: VS Code keeps its settings and state in the page's storage, which
// is the address's (another port is another, empty, VS Code).
const idePort = 47823

var (
	ideTermWait   = 2 * time.Second // SIGTERM, then SIGKILL
	ideProbeEvery = time.Second     // the readiness probe's pace
)

var (
	errIDEQuitting = errors.New("the app is quitting")
	errIDEOtherApp = errors.New("VS Code's folder is another acpost's, running beside this one (an installed app and a build, two launches): its IDE is there; quit it to have the IDE here")
)

// ideStart is one start of the server: its result, ready once it is known.
type ideStart struct {
	ready chan struct{}
	ctx   context.Context // its deadline
	base  string          // http://127.0.0.1:PORT/?tkn=… (set before ready is closed)
	run   *ideRun         // its process (when it served)
	err   error
}

// ideRun is one process of the server, with what is its own.
type ideRun struct {
	cmd    *exec.Cmd
	out    *ideOutput
	exited chan struct{} // closed once the CLI has ended: its address (the CLI's own listener) is dead
	done   chan struct{} // closed once its whole group is gone and its files are removed
}

type ideServer struct {
	mu         sync.Mutex
	cur        *ideStart // the latest start
	run        *ideRun   // the latest process
	life       context.Context
	end        context.CancelFunc // ends life: the app is quitting
	stopped    bool               // nothing starts any more
	looked     bool               // the app's look is installed (else tried again on IDEStart)
	installing bool
	inst       chan struct{} // one install at a time, the first's and any again
	owner      *os.File      // the IDE folder's lock, held while this app owns its server
	claim      sync.Mutex    // held from taking the lock to the end of the orphan's removal; stop waits for it
	stuck      int           // an orphan's process group that did not end (under claim): no start till it is gone

	pane  sync.Mutex // the pane's calls, one at a time
	shown string     // the address the pane has open (under pane)

	// For tests; zero: the real ones.
	findCLI func() (string, error)
	dataDir func() (string, error)
	install func(ctx context.Context, data string) error
	port    int
	budget  time.Duration
	starts  func(port int) // called just before the server starts on port
	owned   func()         // called once the lock is taken (or not), before the orphan is looked for
}

var ide ideServer

var (
	ideURL = regexp.MustCompile(`https?://(?:127\.0\.0\.1|localhost):\d+/?\S*`)
	ideTkn = regexp.MustCompile(`tkn=[^&\s]*`)
)

// vscodeCLI finds VS Code's command line (the app's code-tunnel, for serve-web); or code on PATH.
func vscodeCLI() (string, error) {
	home, _ := os.UserHomeDir()
	for _, a := range []string{"/Applications/Visual Studio Code.app", filepath.Join(home, "Applications/Visual Studio Code.app"),
		"/Applications/Visual Studio Code - Insiders.app", filepath.Join(home, "Applications/Visual Studio Code - Insiders.app")} {
		for _, bin := range []string{"code-tunnel", "code-tunnel-insiders"} {
			p := filepath.Join(a, "Contents/Resources/app/bin", bin)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, nil
			}
		}
	}
	if p, err := exec.LookPath("code"); err == nil {
		return p, nil
	}
	return "", errors.New("VS Code is not installed (Visual Studio Code.app in Applications)")
}

func closed(c chan struct{}) bool {
	if c == nil {
		return false
	}
	select {
	case <-c:
		return true
	default:
		return false
	}
}

// IDEStart gives the address to open folder in, starting VS Code's server once (the first start
// fetches it: a minute or two). Calls meanwhile wait for that same start and read its result; a
// server that ended, or a start that failed, is started again.
func (n *Node) IDEStart(folder string) (string, error) { return ide.open(folder) }

func (s *ideServer) open(folder string) (string, error) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return "", errIDEQuitting
	}
	if s.life == nil {
		s.life, s.end = context.WithCancel(context.Background())
		s.inst = make(chan struct{}, 1)
	}
	st := s.cur
	// st.run is read only once ready is closed (written before it).
	if st == nil || (closed(st.ready) && (st.err != nil || closed(st.run.exited))) {
		budget := s.budget
		if budget == 0 {
			budget = ideBudget
		}
		ctx, cancel := context.WithTimeout(s.life, budget)
		st = &ideStart{ready: make(chan struct{}), ctx: ctx}
		s.cur = st
		go func() {
			defer cancel()
			st.base, st.run, st.err = s.launch(ctx)
			close(st.ready)
		}()
	} else if closed(st.ready) && !s.looked && !s.installing {
		// The server is there but the app's look did not install: tried again, beside it.
		s.installing = true
		go s.reinstall()
	}
	s.mu.Unlock()
	select {
	case <-st.ready:
	case <-st.ctx.Done():
		// Every wait of the start ends with its deadline; this is only a guard.
		select {
		case <-st.ready:
		case <-time.After(15 * time.Second):
			return "", errors.New("VS Code's server did not start in time")
		}
	}
	if st.err != nil {
		return "", st.err
	}
	return withFolder(st.base, folder), nil
}

// launch runs the server and waits for it to serve VS Code, its end, or ctx (the start's
// deadline, the app quitting); what it started is ended when it fails.
func (s *ideServer) launch(ctx context.Context) (string, *ideRun, error) {
	findCLI, dataDir := s.findCLI, s.dataDir
	if findCLI == nil {
		findCLI = vscodeCLI
	}
	if dataDir == nil {
		dataDir = ideDataDir
	}
	cli, err := findCLI()
	if err != nil {
		return "", nil, err
	}
	// The app's own VS Code: its server, settings and extensions kept apart from the VS Code of
	// this Mac (only its engine is used), in the app's support folder.
	data, err := dataDir()
	if err != nil {
		return "", nil, err
	}
	// The server before this one ends first: the two would share VS Code's folders and port.
	s.mu.Lock()
	prev := s.run
	s.mu.Unlock()
	if prev != nil {
		prev.kill()
		if !closed(prev.done) {
			return "", nil, fmt.Errorf("VS Code's previous server (process group %d) has not ended: not starting another beside it", prev.cmd.Process.Pid)
		}
	}
	port, err := s.choosePort(data)
	if err != nil {
		return "", nil, err
	}
	for {
		base, run, err := s.serveOn(ctx, cli, data, port)
		// The port was free when looked at and taken before the server listened (another program
		// starting at the same moment): the server picks a free one itself, within the same deadline.
		if errors.Is(err, errIDEPortLost) && port != 0 && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "acpost: IDE: port %d was taken as VS Code started; it starts on another, without its settings and state\n", port)
			port = 0
			continue
		}
		return base, run, err
	}
}

// errIDEPortLost: the server ended as it started, its port taken by another.
var errIDEPortLost = errors.New("VS Code's port was taken as it started")

// serveOn runs the server on port (0: one it picks) and waits for it to serve VS Code, its end, or
// ctx; what it started is ended when it fails.
func (s *ideServer) serveOn(ctx context.Context, cli, data string, port int) (string, *ideRun, error) {
	token, tokFile, err := ideTokenFile()
	if err != nil {
		return "", nil, err
	}
	out := &ideOutput{token: token, found: make(chan string, 1)}
	cmd := exec.Command(cli, "serve-web", "--host", "127.0.0.1", "--port", strconv.Itoa(port), "--connection-token-file", tokFile,
		"--accept-server-license-terms", "--disable-telemetry",
		"--server-data-dir", filepath.Join(data, "server"), "--cli-data-dir", filepath.Join(data, "cli"),
		"--commit-id", ideCommit)
	// A group of its own: VS Code's server and its helpers end with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stdout, cmd.Stderr = out, out
	// Its end is its own process's: output still held open by what it left is not waited for.
	cmd.WaitDelay = time.Second
	// Started and published under the lock stop takes: a quit either comes first (nothing starts)
	// or finds the process to end.
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		os.Remove(tokFile)
		return "", nil, errIDEQuitting
	}
	if s.starts != nil {
		s.starts(port)
	}
	if err := cmd.Start(); err != nil {
		s.mu.Unlock()
		os.Remove(tokFile)
		return "", nil, err
	}
	run := &ideRun{cmd: cmd, out: out, exited: make(chan struct{}), done: make(chan struct{})}
	s.run = run
	pidFile := filepath.Join(data, idePidFile)
	// The record of the server, published whole (a new file renamed over the old one) or not at
	// all: a server it does not name could not be found again after a crash, so it is not kept.
	rec, recErr := ideWriteRecord(data, cmd.Process.Pid)
	s.mu.Unlock()
	go run.wait(tokFile, pidFile, string(rec))

	fail := func(err error) (string, *ideRun, error) {
		run.kill()
		return "", nil, err
	}
	if recErr != nil {
		return fail(fmt.Errorf("VS Code's server is not recorded (%w): not kept running", recErr))
	}
	select {
	case u := <-out.found:
		base, err := ideBase(u, token)
		if err != nil {
			return fail(err)
		}
		if port == 0 {
			fmt.Fprintln(os.Stderr, "acpost: IDE: VS Code is on", strings.SplitN(strings.TrimPrefix(base, "http://"), "/", 2)[0])
		}
		// The first time the server fetches VS Code itself, and its page says so meanwhile: the
		// address is given once it serves VS Code (the interface shows its own wait till then).
		if err := ideServing(ctx, base, run.exited); err != nil {
			return fail(out.with(s.why(ctx, err)))
		}
		// The app's look for it, installed (or brought up to date) now that VS Code's server is
		// there, within the same deadline; without it VS Code is plain, not broken (and it is
		// tried again on the next IDEStart).
		s.lookInstall(ctx, data)
		return base, run, nil
	case <-run.exited:
		run.kill() // its group: ended, waited for
		if port != 0 && !portFree(port) {
			return "", nil, errIDEPortLost
		}
		return "", nil, out.with(errors.New("VS Code's server stopped as it started"))
	case <-ctx.Done():
		return fail(out.with(s.why(ctx, ctx.Err())))
	}
}

// why names a start's failure: the app quitting and the deadline by their names.
func (s *ideServer) why(ctx context.Context, err error) error {
	switch {
	case s.life.Err() != nil:
		return errIDEQuitting
	case ctx.Err() != nil:
		return errors.New("VS Code's server did not start in time")
	}
	return err
}

// lookInstall installs the app's look, one install at a time (the start's and a later one again:
// two of VS Code's installers would replace the same folder under each other).
func (s *ideServer) lookInstall(ctx context.Context, data string) {
	install := s.install
	if install == nil {
		install = ideInstall
	}
	select {
	case s.inst <- struct{}{}:
	case <-ctx.Done():
		fmt.Fprintln(os.Stderr, "acpost: IDE: the app's look not installed:", ctx.Err())
		return // looked stays as it was: tried again on the next IDEStart
	}
	err := install(ctx, data)
	<-s.inst
	if err != nil {
		fmt.Fprintln(os.Stderr, "acpost: IDE:", err)
	}
	s.mu.Lock()
	s.looked = err == nil
	s.mu.Unlock()
}

// reinstall tries the app's look again for the server that is there.
func (s *ideServer) reinstall() {
	defer func() {
		s.mu.Lock()
		s.installing = false
		s.mu.Unlock()
	}()
	dataDir := s.dataDir
	if dataDir == nil {
		dataDir = ideDataDir
	}
	data, err := dataDir()
	if err != nil {
		return
	}
	s.mu.Lock()
	life := s.life
	s.mu.Unlock()
	s.lookInstall(life, data)
}

// ideTokenFile writes a token of this run into a file of the server's own: created new (never
// another's, never a link), 0600, removed when the server has ended.
func ideTokenFile() (token, name string, err error) {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	token = hex.EncodeToString(b)
	f, err := os.CreateTemp("", "acpost-vscode-token-*")
	if err != nil {
		return "", "", err
	}
	_, werr := f.WriteString(token)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		os.Remove(f.Name())
		return "", "", werr
	}
	return token, f.Name(), nil
}

// ideBase is the server's address u with this run's token (VS Code answers it with a cookie).
func ideBase(u, token string) (string, error) {
	pu, err := url.Parse(u)
	if err != nil {
		return "", err
	}
	q := pu.Query()
	q.Set("tkn", token)
	return pu.Scheme + "://" + pu.Host + "/?" + q.Encode(), nil
}

// wait is the one place a process's end is seen: what is left of its group is ended, its token
// and pid files removed (the pid file only while it is still this process's record, pid), then
// done closed.
func (r *ideRun) wait(tokFile, pidFile, pid string) {
	_ = r.cmd.Wait()
	close(r.exited)
	// The CLI's end is not its group's: VS Code's own server may still be ending (or, the CLI
	// gone by itself, running on). The group gets the same time to end in order, then is killed;
	// done is closed once it is gone, its port free: not before, however long that is (a process
	// stuck past SIGKILL keeps its group, and a new start is refused meanwhile).
	pg := r.cmd.Process.Pid
	if !groupGone(pg, 0) {
		_ = syscall.Kill(-pg, syscall.SIGTERM)
		if !groupGone(pg, ideTermWait) {
			for !groupGone(pg, 3*time.Second) {
				_ = syscall.Kill(-pg, syscall.SIGKILL)
			}
		}
	}
	os.Remove(tokFile)
	if b, err := os.ReadFile(pidFile); err == nil && pid != "" && string(b) == pid {
		os.Remove(pidFile)
	}
	close(r.done)
}

// kill ends the process's group and waits for its end: SIGTERM, then SIGKILL if it is still
// there after ideTermWait.
func (r *ideRun) kill() {
	if closed(r.done) {
		return
	}
	pid := r.cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	select {
	case <-r.done:
		return
	case <-time.After(ideTermWait):
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	select {
	case <-r.done:
	case <-time.After(ideTermWait + 5*time.Second):
		// A group still there past SIGKILL: given up here; done stays open until it is gone (and a
		// new start is refused till then).
	}
}

// ideStuckGroup is a process group taken as never gone (for tests: a process SIGKILL does not end).
var ideStuckGroup atomic.Int64

// groupGone waits up to d for the process group pg to have no process left.
func groupGone(pg int, d time.Duration) bool {
	for end := time.Now().Add(d); ; time.Sleep(20 * time.Millisecond) {
		if ideStuckGroup.Load() != int64(pg) && syscall.Kill(-pg, 0) == syscall.ESRCH {
			return true
		}
		if !time.Now().Before(end) {
			return false
		}
	}
}

// ideOutput reads the server's output: the first address it prints, and its last lines (the
// token taken out) for a start's error. Lines are kept to ideLineMax; the rest of a longer one is
// read and dropped, so the pipe never fills.
type ideOutput struct {
	mu    sync.Mutex
	token string
	found chan string
	line  []byte
	tail  []string
}

const (
	ideLineMax   = 4 << 10
	ideTailLines = 20
)

func (o *ideOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		chunk := p
		if i >= 0 {
			chunk = p[:i]
		}
		if room := ideLineMax - len(o.line); room > 0 {
			o.line = append(o.line, chunk[:min(room, len(chunk))]...)
		}
		if i < 0 {
			break
		}
		o.addLine(string(bytes.TrimRight(o.line, "\r")))
		o.line = o.line[:0]
		p = p[i+1:]
	}
	return n, nil
}

func (o *ideOutput) addLine(l string) {
	if m := ideURL.FindString(l); m != "" {
		select {
		case o.found <- m:
		default:
		}
	}
	if l = o.redact(l); strings.TrimSpace(l) != "" {
		if len(l) > 300 {
			l = l[:300] + "…"
		}
		o.tail = append(o.tail, l)
		if len(o.tail) > ideTailLines {
			o.tail = o.tail[1:]
		}
	}
}

func (o *ideOutput) redact(l string) string {
	l = ideTkn.ReplaceAllString(l, "tkn=…")
	if o.token != "" {
		l = strings.ReplaceAll(l, o.token, "…")
	}
	return l
}

// with is err with the server's last lines (what it said of why).
func (o *ideOutput) with(err error) error {
	o.mu.Lock()
	lines := append([]string(nil), o.tail...)
	if l := o.redact(string(o.line)); strings.TrimSpace(l) != "" {
		lines = append(lines, l[:min(len(l), 300)])
	}
	o.mu.Unlock()
	if len(lines) == 0 || errors.Is(err, errIDEQuitting) {
		return err
	}
	return fmt.Errorf("%w; VS Code said:\n%s", err, strings.Join(lines, "\n"))
}

// ideServing waits until the server at base serves VS Code's workbench, the server ends, or ctx.
func ideServing(ctx context.Context, base string, done chan struct{}) error {
	// VS Code answers the token with a cookie and a redirect to /: kept, as the page keeps it.
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Timeout: 10 * time.Second, Jar: jar}
	for {
		if req, err := http.NewRequestWithContext(ctx, http.MethodGet, base, nil); err == nil {
			if r, err := c.Do(req); err == nil {
				b, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				r.Body.Close()
				if err == nil && r.StatusCode == http.StatusOK && ideServes(b) {
					return nil
				}
			}
		}
		select {
		case <-done:
			return errors.New("VS Code's server stopped as it started")
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ideProbeEvery):
		}
	}
}

// ideServes tells VS Code's page from the "downloading" one the server shows while it fetches
// VS Code, or any other: by the pinned workbench's configuration element.
func ideServes(page []byte) bool {
	return bytes.Contains(page, []byte(`id="vscode-workbench-web-configuration"`))
}

// withFolder is base with folder to open.
func withFolder(base, folder string) string {
	u, _ := url.Parse(base)
	q := u.Query()
	if folder != "" && filepath.IsAbs(folder) {
		q.Set("folder", folder)
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// stop ends VS Code's server (with the app) and waits for its end; a start under way ends with
// it, and nothing starts after.
func (s *ideServer) stop() {
	s.mu.Lock()
	s.stopped = true
	if s.end != nil {
		s.end()
	}
	run := s.run
	s.mu.Unlock()
	if run != nil {
		run.kill()
	}
	s.claim.Lock() // an orphan's removal under the lock finishes first
	s.disown()
	s.claim.Unlock()
}

// own takes the IDE folder's lock for this app's life, when no other running app of this kind
// holds it: the one that holds it owns the server the pid file names. The lock ends with the
// process, so a crashed app's server is no one's: the next app takes the lock and ends it. An
// app that is quitting lets it go in a moment: it is waited for that long (ideOwnWait).
func (s *ideServer) own(data string) bool {
	for end := time.Now().Add(ideOwnWait); ; time.Sleep(50 * time.Millisecond) {
		if owned, done := s.tryOwn(data); done || !time.Now().Before(end) {
			return owned
		}
	}
}

// ideOwnWait is how long a start waits for another app to let the IDE folder go.
var ideOwnWait = 3 * time.Second

// tryOwn is one try at the lock: whether it is held, and whether trying again is of no use.
func (s *ideServer) tryOwn(data string) (owned, done bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner != nil {
		return true, true
	}
	if s.stopped { // quitting: nothing to own
		return false, true
	}
	f, err := os.OpenFile(filepath.Join(data, ideLockFile), os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return false, true
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return false, false
	}
	s.owner = f
	return true, true
}

// disown lets the lock go (the app quitting; its server has ended).
func (s *ideServer) disown() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.owner != nil {
		s.owner.Close()
		s.owner = nil
	}
}

// idePidFile, in the IDE's folder, names the server's process while it runs: a server left by an
// app that did not end it (it crashed, it was killed) is found by it and ended.
const idePidFile = "serve-web.pid"

// ideLockFile, beside it, is held by the app whose server that is (see own).
const ideLockFile = "serve-web.lock"

// choosePort is the IDE's port, when it is free or taken by a server of this app's left from
// before (ended then); else, a free one (VS Code's state there is another's: said so).
func (s *ideServer) choosePort(data string) (int, error) {
	want := s.port
	if want == 0 {
		want = idePort
	}
	// Only the lock's holder starts a server: the folder's server, settings, extensions and record
	// are its. Another app running beside it (an installed app and a build of it, two launches)
	// starts none: a server of its would share all that, and no record would name it.
	// A server no running app owns (its app crashed) is ended first, whatever port it is on (it
	// may have started on another one; its folder is this one): its group waited for.
	// The lock is held throughout (stop lets it go only after): the pid file read is the one this
	// app's lock covers, never one another app wrote after taking the lock.
	s.claim.Lock()
	owner := s.own(data)
	if s.owned != nil {
		s.owned()
	}
	if !owner {
		s.claim.Unlock()
		if s.life.Err() != nil {
			return 0, errIDEQuitting
		}
		return 0, errIDEOtherApp
	}
	if s.stuck != 0 && groupGone(s.stuck, 0) {
		s.stuck = 0
	}
	if s.stuck == 0 {
		if pid := ideOrphan(data); pid > 0 && !endGroup(pid) {
			s.stuck = pid
		}
	}
	if s.stuck != 0 {
		pg := s.stuck
		s.claim.Unlock()
		return 0, fmt.Errorf("VS Code's server left from before (process group %d) does not end: not starting another beside it", pg)
	}
	s.claim.Unlock()
	if portFree(want) {
		return want, nil
	}
	// Taken: the server picks a free port itself as it listens (one picked here could be taken
	// before it starts); its address says which.
	fmt.Fprintf(os.Stderr, "acpost: IDE: port %d is taken by something else; VS Code starts on another, without its settings and state\n", want)
	return 0, nil
}

func portFree(port int) bool {
	l, err := net.Listen("tcp", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

// ideOrphan is the process group the pid file names when it is provably this app's server for
// data, else 0 (nothing is signalled on a guess). Proof: the record is of this boot, and either its
// leader runs, started when the record says, leads its own group, and is serve-web with this
// server folder; or the leader is gone and every process left in its group is VS Code's of this
// folder (a group's id is not taken again while it has a process, but the number may have been
// taken again since the server ended: by a group whose leader has gone too).
func ideOrphan(data string) int {
	b, err := os.ReadFile(filepath.Join(data, idePidFile))
	if err != nil {
		return 0
	}
	var rec ideRecord
	if json.Unmarshal(b, &rec) != nil || rec.Pid <= 1 || rec.Start == "" || rec.Boot == "" || rec.Boot != ideBoot() {
		return 0
	}
	pid := rec.Pid
	g, start, ok := ideProc(pid)
	if !ok {
		if ideGroupOf(pid, data) {
			return pid
		}
		return 0
	}
	if g != pid || start != rec.Start {
		return 0
	}
	cmdline, err := idePS("-ww", "-o", "command=", "-p", strconv.Itoa(pid))
	if err != nil {
		return 0
	}
	c := string(cmdline)
	if !strings.Contains(c, " serve-web ") || !strings.Contains(c, "--server-data-dir "+filepath.Join(data, "server")) {
		return 0
	}
	return pid
}

// ideMembers gives the command lines of the processes in group pg (for tests: a group made up).
var ideMembers = func(pg int) []string {
	out, err := idePS("-A", "-ww", "-o", "pgid=,command=")
	if err != nil {
		return nil
	}
	var cmds []string
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == strconv.Itoa(pg) {
			cmds = append(cmds, strings.Join(f[1:], " "))
		}
	}
	return cmds
}

// ideGroupOf is whether the leaderless group pg is what is left of a server of data's: it has
// processes, and each names data's folder in its command line (VS Code's server is run from the
// CLI's folder there, with its server folder there).
func ideGroupOf(pg int, data string) bool {
	cmds := ideMembers(pg)
	if len(cmds) == 0 {
		return false
	}
	for _, c := range cmds {
		if !strings.Contains(c, filepath.Join(data)+string(filepath.Separator)) {
			return false
		}
	}
	return true
}

// endGroup ends the group pid leads: SIGTERM, then SIGKILL; whether it is gone.
func endGroup(pid int) bool {
	_ = syscall.Kill(-pid, syscall.SIGTERM)
	if groupGone(pid, 3*time.Second) {
		return true
	}
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	return groupGone(pid, 2*time.Second)
}

// IDEShow lays the IDE over the box at x, y, w × h (window points) and opens url in it when given.
// The pane's calls are taken one at a time (pane): each is queued for the main thread in the
// order taken, with what is open checked and set in the same step.
func (n *Node) IDEShow(url string, x, y, w, h float64) error {
	if n.win == nil {
		return errPreview
	}
	if data, err := ideDataDir(); err == nil { // the scripts, before the pane is made
		if p := ideExtension(); p.err == nil {
			ideScripts(ideExtDir(data, p.version), p.hash)
		}
	}
	ide.pane.Lock()
	defer ide.pane.Unlock()
	// The same address again (the IDE shown once more): VS Code stays as it is, not reloaded; focus
	// goes into VS Code when it is opened, not each time the pane is placed.
	u := strings.TrimSpace(url)
	load := u != "" && u != ide.shown
	if err := preview.IDEShow(n.win.NativeWindow(), x, y, w, h, load); err != nil {
		return err
	}
	if load {
		if err := preview.IDELoad(u); err != nil {
			return err
		}
		ide.shown = u
	}
	return nil
}

// IDEReload loads the IDE's page again (the interface asks IDEStart first: that starts the server
// again if it ended, and its answer is checked against what is shown before anything navigates).
func (n *Node) IDEReload() {
	ide.pane.Lock()
	defer ide.pane.Unlock()
	preview.IDEReload()
}

// IDETheme gives the IDE the app's theme: VS Code follows light or dark.
func (n *Node) IDETheme(dark bool) { preview.IDEAppearance(dark) }

// IDEHide hides it; VS Code keeps running.
func (n *Node) IDEHide() {
	ide.pane.Lock()
	defer ide.pane.Unlock()
	preview.IDEHide()
}

// Menu shows a menu of the system's at x, y (window points) — over the native panes, which an
// HTML menu would be under — and gives the item chosen, or -1 (closed; or no window: the
// interface then shows its own).
func (n *Node) Menu(x, y float64, titles, keys []string, checked int) int {
	if n.win == nil || n.win.NativeWindow() == nil {
		return -2
	}
	return preview.Popup(n.win.NativeWindow(), x, y, titles, keys, checked)
}

// ideDataDir is where the app's VS Code keeps its server, settings and extensions.
func ideDataDir() (string, error) {
	base, err := os.UserConfigDir() // ~/Library/Application Support
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "acpost", "vscode")
	return d, os.MkdirAll(d, 0o700)
}

// ideRecord is what the pid file says of the server: its process, the boot it ran in and when its
// process started. A process of that number in another boot, or started at another time, is not
// it (numbers are taken again).
type ideRecord struct {
	Pid   int    `json:"pid"`
	Boot  string `json:"boot"`
	Start string `json:"start"`
}

// ideBoot names this boot of the Mac.
func ideBoot() string {
	b, _ := syscall.Sysctl("kern.bootsessionuuid")
	return b
}

// idePS runs ps in the C locale (its dates read the same in every app that reads them).
func idePS(args ...string) ([]byte, error) {
	c := exec.Command("ps", args...)
	c.Env = append(os.Environ(), "LC_ALL=C")
	return c.Output()
}

// ideProc is what ps says of pid: its process group and the time it started; ok is false when
// there is no such process.
func ideProc(pid int) (pgid int, start string, ok bool) {
	out, err := idePS("-o", "pgid=,lstart=", "-p", strconv.Itoa(pid))
	f := strings.Fields(string(out))
	if err != nil || len(f) < 2 {
		return 0, "", false
	}
	pgid, err = strconv.Atoi(f[0])
	if err != nil {
		return 0, "", false
	}
	return pgid, strings.Join(f[1:], " "), true
}

// ideWriteRecord names the server pid in the pid file in data: written to a new file and renamed
// over the old one (a reader sees the old record or the new, never part of one). It gives what it
// wrote.
func ideWriteRecord(data string, pid int) ([]byte, error) {
	_, start, ok := ideProc(pid)
	boot := ideBoot()
	if !ok || boot == "" {
		return nil, errors.New("its process is not known to ps")
	}
	b, err := json.Marshal(ideRecord{Pid: pid, Boot: boot, Start: start})
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(data, ".serve-web.pid-*")
	if err != nil {
		return nil, err
	}
	_, werr := f.Write(b)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(f.Name(), filepath.Join(data, idePidFile))
	}
	if werr != nil {
		os.Remove(f.Name())
		return nil, werr
	}
	return b, nil
}
