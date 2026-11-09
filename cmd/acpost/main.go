// Command acpost sends messages between coding agents and runs the node that delivers them.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/config"
	"github.com/k1dory/acpost/internal/node"
	"github.com/k1dory/acpost/internal/store"
)

// Exit codes.
const (
	exitOK          = 0
	exitError       = 1
	exitUsage       = 2
	exitNotFound    = 3
	exitTimeout     = 4
	exitFailed      = 5
	exitNoNode      = 6
	exitRefused     = 7
	exitInterrupted = 130 // as a shell says a command ended by SIGINT
)

type exitErr struct {
	code int
	err  error
	name string // error code for --json ("" : from the exit code)
	hint string
}

func (e *exitErr) Error() string { return e.err.Error() }
func (e *exitErr) Unwrap() error { return e.err }

func usageErr(err error) error { return &exitErr{code: exitUsage, err: err} }

type globals struct {
	node, token, config string
	json                bool
	explicitNode        bool
	cancel              context.CancelFunc // of a quick command's budget
	status              *client.Status     // what connect's probe was told (nil: not asked)
	flagErr             bool               // the flags did not parse: g.json is as far as cobra got
}

// How long the CLI waits for a node that took the connection but does not answer: a probe of the
// local node (then it is said to be stuck, not started again), and a whole quick command (one
// that only asks: status, list…). Commands that wait by design (wait, send --wait, recv --wait,
// show --follow, term, forge open/merge waiting for an approval) keep their own budgets.
const (
	probeTimeout = 5 * time.Second
	quickTimeout = 30 * time.Second
)

// quick marks a command whose whole run is bounded by quickTimeout.
var quick = map[string]string{"quick": "1"}

func (g *globals) configPath() string {
	p := g.config
	if p == "" {
		p = config.DefaultPath()
	}
	// Absolute: a node started in the background runs from another directory.
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

func main() {
	g := &globals{}
	root := rootCmd(g) // registering the flags sets their defaults
	// Known before flags are parsed: an error in parsing them is reported in JSON too. Kept apart
	// from g.json, which cobra sets as it goes and leaves where a bad flag stopped it.
	parseJSON := jsonWanted(os.Args[1:], os.Getenv("ACPOST_JSON"))
	g.json = parseJSON
	// Ctrl-C ends a command through its context: term restores the terminal and closes its own,
	// a save is not cut midway; a second Ctrl-C ends it at once.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	go func() { <-ctx.Done(); stop() }()
	err := root.ExecuteContext(ctx)
	if err != nil && (ctx.Err() != nil && errors.Is(err, context.Canceled) || errors.Is(err, errInterrupted)) {
		err = &exitErr{code: exitInterrupted, name: "interrupted", err: errors.New("interrupted")}
	}
	if err == nil {
		return
	}
	code := exitCode(err)
	hint := hintOf(err)
	if g.flagErr {
		g.json = parseJSON // the last --json given decides, wherever the bad flag was
	}
	if g.json {
		e := map[string]any{"code": codeName(err), "message": err.Error()}
		if hint != "" {
			e["hint"] = hint
		}
		b, _ := json.Marshal(map[string]any{"error": e})
		fmt.Fprintln(os.Stderr, string(b))
	} else {
		fmt.Fprintln(os.Stderr, "acpost:", err)
		if hint != "" {
			fmt.Fprintln(os.Stderr, "  hint:", hint)
		}
	}
	os.Exit(code)
}

// codes are the error codes a node says (and --json shows) and the exit codes they are.
var codes = []struct {
	exit int
	name string
}{
	{exitUsage, "invalid"}, {exitNotFound, "not_found"}, {exitTimeout, "timeout"},
	{exitFailed, "failed"}, {exitNoNode, "unavailable"}, {exitRefused, "refused"},
}

func exitCode(err error) int {
	var ee *exitErr
	if errors.As(err, &ee) {
		return ee.code
	}
	var ce *client.Error
	if errors.As(err, &ce) {
		for _, c := range codes {
			if c.name == ce.Code && c.exit != exitFailed { // a message fails; a request does not
				return c.exit
			}
		}
		return exitError
	}
	if errors.Is(err, client.ErrUnavailable) || errors.Is(err, context.DeadlineExceeded) {
		return exitNoNode
	}
	if isFlagErr(err) {
		return exitUsage
	}
	return exitError
}

func hintOf(err error) string {
	var ee *exitErr
	if errors.As(err, &ee) && ee.hint != "" {
		return ee.hint
	}
	var ce *client.Error
	if errors.As(err, &ce) {
		return ce.Hint
	}
	switch {
	case errors.Is(err, syscall.EPERM) || errors.Is(err, syscall.EACCES):
		return "this process may not connect to the node's socket (a sandbox, or file permissions); the node itself may be fine: " +
			"run acpost outside the sandbox (allow the command), or reach the node over TCP with --node http://127.0.0.1:PORT --token …"
	case errors.Is(err, client.ErrUnavailable):
		return "no node answers there: `acpost serve` starts one, or check --node / ACPOST_NODE"
	case errors.Is(err, context.DeadlineExceeded):
		return "the node took the request but did not answer in time: it may be stuck (its log says), or the network slow"
	case exitCode(err) == exitUsage:
		return "`acpost <command> --help` shows the flags"
	}
	return ""
}

func codeName(err error) string {
	var ee *exitErr
	if errors.As(err, &ee) && ee.name != "" {
		return ee.name
	}
	var ce *client.Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	code := exitCode(err)
	for _, c := range codes {
		if c.exit == code {
			return c.name
		}
	}
	return "error"
}

// isFlagErr is cobra's refusal of an unknown command (flags and arguments are wrapped: see
// usageArgs and SetFlagErrorFunc).
func isFlagErr(err error) bool {
	return strings.HasPrefix(err.Error(), "unknown command")
}

// jsonWanted says whether the output is JSON: the last --json flag given (--json, --json=BOOL),
// else ACPOST_JSON=1.
func jsonWanted(args []string, env string) bool {
	want := env == "1"
	for _, a := range args {
		if a == "--" {
			break
		}
		if a == "--json" {
			want = true
		} else if v, ok := strings.CutPrefix(a, "--json="); ok {
			if b, err := strconv.ParseBool(v); err == nil {
				want = b
			}
		}
	}
	return want
}

// usageArgs makes the argument checks of a command and those under it usage errors (exit 2).
func usageArgs(c *cobra.Command) {
	if check := c.Args; check != nil {
		c.Args = func(cmd *cobra.Command, args []string) error {
			if err := check(cmd, args); err != nil {
				return usageErr(err)
			}
			return nil
		}
	}
	for _, sub := range c.Commands() {
		usageArgs(sub)
	}
}

func rootCmd(g *globals) *cobra.Command {
	root := &cobra.Command{
		Use:           "acpost",
		Short:         "Asynchronous messages between coding agents (ACP)",
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       node.Version,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			g.explicitNode = g.node != ""
			if g.node == "" {
				g.node = os.Getenv("ACPOST_NODE")
				g.explicitNode = g.node != ""
			}
			if g.token == "" {
				g.token = os.Getenv("ACPOST_TOKEN")
			}
			if !cmd.Flags().Changed("json") {
				g.json = os.Getenv("ACPOST_JSON") == "1" // the flag, when given, wins
			}
			if g.config == "" {
				g.config = os.Getenv("ACPOST_CONFIG")
			}
			if cmd.Annotations["quick"] != "" {
				ctx, cancel := context.WithTimeout(cmd.Context(), quickTimeout)
				g.cancel = cancel
				cmd.SetContext(ctx)
			}
			return nil
		},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { g.flagErr = true; return usageErr(err) })
	pf := root.PersistentFlags()
	pf.StringVar(&g.node, "node", "", "node to talk to: unix:///path, http(s)://host:port (ACPOST_NODE; default: the local node)")
	pf.StringVar(&g.token, "token", "", "bearer token (ACPOST_TOKEN: given here it shows in ps, the variable does not)")
	pf.BoolVar(&g.json, "json", false, "JSON output (ACPOST_JSON=1)")
	pf.StringVar(&g.config, "config", "", "configuration file (ACPOST_CONFIG; default ~/.config/acpost/config.yaml)")

	msgCmds := func() []*cobra.Command {
		return []*cobra.Command{sendCmd(g), waitCmd(g), showCmd(g), listCmd(g), cancelCmd(g), recvCmd(g), replyCmd(g)}
	}
	root.AddCommand(msgCmds()...)
	message := &cobra.Command{Use: "message", Short: "The message commands again (acpost message send … = acpost send …)"}
	message.AddCommand(msgCmds()...)
	root.AddCommand(message, filesCmd(g), attachCmd(g), agentsCmd(g), sessionsCmd(g), statusCmd(g), usageCmd(g), changesCmd(g), termCmd(g), forgeCmd(g), conversationsCmd(g), approveCmd(g), serveCmd(g))
	usageArgs(root)
	return root
}

// connect returns a client for the node, starting the local node first if it is not running and
// none was named explicitly.
func (g *globals) connect(ctx context.Context) (*client.Client, error) {
	url, cfg, err := g.nodeURL()
	if err != nil {
		return nil, err
	}
	c, err := client.New(url, g.token, "")
	if err != nil {
		return nil, usageErr(err)
	}
	if g.explicitNode || cfg == nil || !strings.HasPrefix(url, "unix://") {
		return c, nil
	}
	st, err := probe(ctx, c)
	if err == nil {
		g.status = &st
	}
	if err == nil || !errors.Is(err, client.ErrUnavailable) || errors.Is(err, context.DeadlineExceeded) {
		if errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil {
			// Something holds the socket and does not answer: a stuck node, not a missing one
			// (another started now would find its store taken).
			return nil, &exitErr{code: exitNoNode, name: "unavailable", err: fmt.Errorf("the node at %s does not answer (%s)", url, probeTimeout),
				hint: "it may be stuck: its log is " + filepath.Join(filepath.Dir(strings.TrimPrefix(url, "unix://")), "acpost.log")}
		}
		return c, nil
	}
	if ctx.Err() != nil {
		return nil, ctx.Err() // interrupted while asking: not a node to start
	}
	if err := autostart(ctx, g, c, strings.TrimPrefix(url, "unix://")); err != nil {
		return nil, err
	}
	return c, nil
}

func localURL(cfg *config.Config) string {
	for _, l := range cfg.Node.Listen {
		if strings.HasPrefix(l, "unix://") {
			return l
		}
	}
	scheme := "http://"
	if cfg.Node.TLS != nil {
		scheme = "https://"
	}
	return scheme + strings.TrimPrefix(cfg.Node.Listen[0], "tcp://")
}

// autostart runs `acpost serve` in the background, detached, and waits for it to answer.
func autostart(ctx context.Context, g *globals, c *client.Client, sock string) error {
	dir := filepath.Dir(sock)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	logf, err := os.OpenFile(filepath.Join(dir, "acpost.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer logf.Close()
	logStart, _ := logf.Seek(0, io.SeekEnd)
	self, err := os.Executable()
	if err != nil {
		return err
	}
	// --config only when one was named: the child finds the implicit default as this command did,
	// and may find it absent (a named file must exist).
	args := []string{"serve"}
	if g.config != "" {
		args = append(args, "--config", g.configPath())
	}
	cmd := exec.Command(self, args...)
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Dir = "/"
	if err := cmd.Start(); err != nil {
		return &exitErr{code: exitNoNode, err: fmt.Errorf("start the local node: %w", err)}
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if ctx.Err() != nil {
			return ctx.Err() // the node goes on starting on its own; this command is interrupted
		}
		if _, err := probe(ctx, c); err == nil {
			// Left running, detached (its own session): not Released, which would race the
			// goroutine waiting for it (the race detector's find).
			return nil
		}
		select {
		case <-exited:
			// Another command may have started a node at the same moment: it holds the store.
			if _, err := probe(ctx, c); err == nil {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
			if _, err := probe(ctx, c); err == nil {
				return nil
			}
			return startFailed(filepath.Join(dir, "acpost.log"), logStart)
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	return &exitErr{code: exitNoNode, err: fmt.Errorf("the local node did not answer within 15s"), hint: "its log is " + filepath.Join(dir, "acpost.log")}
}

// probe asks the node for its status, waiting probeTimeout at most.
func probe(ctx context.Context, c *client.Client) (client.Status, error) {
	pctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	st, err := c.Status(pctx)
	if err != nil && ctx.Err() == nil && errors.Is(pctx.Err(), context.DeadlineExceeded) {
		// Out of time, whatever the client calls it (a body that stops midway is "unavailable"
		// there): the node is there and stuck, not missing.
		return st, fmt.Errorf("%w: %w", context.DeadlineExceeded, err)
	}
	return st, err
}

// startFailed says why the node just started from this command exited: the last line it logged.
func startFailed(log string, from int64) error {
	why, hint := "", "its log is "+log
	if f, err := os.Open(log); err == nil {
		if _, err := f.Seek(from, io.SeekStart); err == nil {
			b, _ := io.ReadAll(io.LimitReader(f, 64<<10))
			lines := strings.Split(strings.TrimSpace(string(b)), "\n")
			why = strings.TrimPrefix(lines[len(lines)-1], "acpost: ")
		}
		f.Close()
	}
	if strings.Contains(why, "in use by another node") {
		hint = "a node on this store is still stopping, or runs from another config: try again in a moment; " + hint
	}
	err := errors.New("the local node did not start")
	if why != "" {
		err = fmt.Errorf("the local node did not start: %s", why)
	}
	return &exitErr{code: exitNoNode, err: err, hint: hint}
}

func printJSON(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

// readBody takes the body from --body, --body-file or stdin (when it is not a terminal).
func readBody(cmd *cobra.Command, body, file string) (string, bool, error) {
	ctx := cmd.Context()
	switch {
	case cmd.Flags().Changed("body"):
		return body, true, nil
	case file == "-":
		b, err := readCtx(ctx, func() ([]byte, error) { return io.ReadAll(os.Stdin) })
		return string(b), true, err
	case file != "":
		b, err := readCtx(ctx, func() ([]byte, error) { return os.ReadFile(file) }) // a FIFO waits too
		return string(b), true, err
	case !term.IsTerminal(int(os.Stdin.Fd())):
		b, err := readCtx(ctx, func() ([]byte, error) { return io.ReadAll(os.Stdin) })
		if err != nil {
			return "", false, err
		}
		return string(b), len(b) > 0, nil
	}
	return "", false, nil
}

// readCtx is read, given up when ctx ends (Ctrl-C while stdin is still open): a read of a pipe
// or a terminal cannot be stopped, so it is left behind for the process's exit to end.
func readCtx(ctx context.Context, read func() ([]byte, error)) ([]byte, error) {
	type got struct {
		b   []byte
		err error
	}
	ch := make(chan got, 1)
	go func() {
		b, err := read()
		ch <- got{b, err}
	}()
	select {
	case r := <-ch:
		return r.b, r.err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// readSecret reads a line from the terminal without echoing it. The terminal's mode is changed
// and put back here, in this goroutine, before and after the read, never by the reader left behind
// on a cancel: a context already ended changes nothing, and one that ends meanwhile finds the mode
// set and puts it back. Raw, a typed Ctrl-C is a byte, read as an interrupt too.
func readSecret(ctx context.Context, fd int) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	old, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	defer term.Restore(fd, old)
	return readCtx(ctx, func() ([]byte, error) {
		var line []byte
		var b [1]byte
		for {
			n, err := syscall.Read(fd, b[:])
			if err == syscall.EINTR {
				continue
			}
			if err != nil {
				return line, err
			}
			if n == 0 {
				return line, io.EOF
			}
			switch b[0] {
			case '\r', '\n':
				return line, nil
			case 3: // Ctrl-C
				return nil, errInterrupted
			case 4: // Ctrl-D
				if len(line) == 0 {
					return nil, io.EOF
				}
			case 127, 8: // erase
				if len(line) > 0 {
					line = line[:len(line)-1]
				}
			default:
				line = append(line, b[0])
			}
		}
	})
}

// errInterrupted is a Ctrl-C read as a byte (a terminal in raw mode sends no signal).
var errInterrupted = errors.New("interrupted")

func sendCmd(g *globals) *cobra.Command {
	var to, body, bodyFile, task, thread, idem, session, save string
	var cwd, model, effort, mode string
	var meta, attach, links []string
	var ttl, timeout time.Duration
	var wait bool
	cmd := &cobra.Command{
		Use:   "send --to ADDR [--body TEXT | --body-file F | stdin] [--attach FILE]...",
		Short: "Send a message; prints its id",
		Example: `  acpost send --to agent:reviewer --task TASK-42 --body "Review PR #123"
  git diff | acpost send --to reviewer --wait --timeout 10m
  acpost send --to reviewer --body "Check the report" --attach report.md --attach shot.png`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if to == "" {
				return usageErr(errors.New("--to is required"))
			}
			if save != "" && !wait {
				return usageErr(errors.New("--save goes with --wait"))
			}
			// Every flag is checked before anything happens: no node started, no file read for a
			// send that a typo refuses.
			if err := nonNegative(map[string]time.Duration{"--ttl": ttl, "--timeout": timeout}); err != nil {
				return err
			}
			var metas map[string]string
			for _, kv := range meta {
				k, v, found := strings.Cut(kv, "=")
				if !found || k == "" {
					return usageErr(fmt.Errorf("--meta %q: k=v", kv))
				}
				if metas == nil {
					metas = map[string]string{}
				}
				metas[k] = v
			}
			text, ok, err := readBody(cmd, body, bodyFile)
			if err != nil {
				return err
			}
			if !ok && len(attach) == 0 && len(links) == 0 {
				return usageErr(errors.New("no body: --body, --body-file, stdin, --attach or --link"))
			}
			ctx := cmd.Context()
			c, err := g.connect(ctx)
			if err != nil {
				return err
			}
			files, err := readFiles(ctx, c, attach)
			if err != nil {
				return err
			}
			linked, err := readLinks(links)
			if err != nil {
				return err
			}
			files = append(files, linked...)
			req := client.SendRequest{To: to, Body: text, Task: task, Thread: thread, Session: session, Files: files,
				Cwd: cwd, Model: model, Effort: effort, Mode: mode, Meta: metas}
			if ttl > 0 {
				req.TTL = ttl.String()
			}
			sctx, done := context.WithTimeout(ctx, requestBudget(req.Body, req.Files))
			m, _, err := c.Send(sctx, req, idem)
			done()
			if err != nil {
				return err
			}
			if wait {
				return waitAndPrint(ctx, g, c, []string{m.ID}, timeout, save)
			}
			if g.json {
				printJSON(m)
			} else {
				fmt.Println(m.ID)
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&to, "to", "", "recipient: agent:NAME, agent:NAME@NODE or NAME")
	f.StringVar(&body, "body", "", "the message")
	f.StringVar(&bodyFile, "body-file", "", "read the message from a file (- for stdin)")
	f.StringArrayVarP(&attach, "attach", "a", nil, "attach a file (repeatable); an ACP agent gets text embedded, images inline, the rest as a path")
	f.StringArrayVar(&links, "link", nil, "attach a link to a file of this machine or an http(s) URL, not a copy (repeatable)")
	f.StringVar(&task, "task", "", "task label; messages of one task share the agent's session")
	f.StringVar(&thread, "thread", "", "thread id")
	f.StringVar(&session, "session", "", "continue this existing ACP session of the agent (an editor's, say)")
	f.StringVar(&cwd, "cwd", "", "a new task's directory (an ACP agent of this node; the thread's next messages stay there)")
	f.StringVar(&model, "model", "", "the model for this message (one the agent offers: acpost options AGENT)")
	f.StringVar(&effort, "effort", "", "the reasoning effort for this message")
	f.StringVar(&mode, "mode", "", "the session mode for this message")
	f.StringArrayVar(&meta, "meta", nil, "metadata k=v (repeatable)")
	f.DurationVar(&ttl, "ttl", 0, "expire if no agent took it within this long")
	f.StringVar(&idem, "idempotency-key", "", "a repeat with the same key returns the first message")
	f.BoolVar(&wait, "wait", false, "wait for the reply and print it")
	f.DurationVar(&timeout, "timeout", 0, "with --wait: give up waiting after this long (exit 4)")
	f.StringVar(&save, "save", "", "with --wait: save the files attached to the reply into this directory")
	return cmd
}

// waitAndPrint waits for each message to be final and prints the replies.
// save, when set, is the directory the replies' files go to.
func waitAndPrint(ctx context.Context, g *globals, c *client.Client, ids []string, timeout time.Duration, save string) error {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	var failed, pending []string
	var last store.Message
	for i, id := range ids {
		left := time.Duration(0)
		if !deadline.IsZero() {
			left = max(time.Until(deadline), time.Millisecond)
		}
		m, err := c.WaitFinal(ctx, id, left)
		var ce *client.Error
		if errors.As(err, &ce) && ce.Code == "timeout" {
			// The long poll itself ran into the deadline (the node answered late): the message as
			// it is now, said as any other one still going (its text so far, the hint to go on).
			gctx, done := context.WithTimeout(ctx, quickTimeout)
			m, err = c.Get(gctx, id)
			done()
		}
		if err != nil {
			return err
		}
		var saved []savedFile
		var saveErr error
		if save != "" && m.State.Final() {
			saved, saveErr = saveReplyFiles(ctx, c, m, save)
		}
		if g.json {
			if save != "" {
				printJSON(struct {
					store.Message
					Saved []savedFile `json:"saved"`
				}{m, saved})
			} else {
				printJSON(m)
			}
		} else {
			if len(ids) > 1 {
				if i > 0 && !strings.HasSuffix(m.Reply, "\n") {
					fmt.Println()
				}
				fmt.Println("--- " + m.ID)
			}
			fmt.Print(m.Reply)
			// A terminal gets a final newline; a pipe gets the reply exactly as it is, and the notes on
			// stderr start a line of their own (2>&1 would glue them to the reply's last line).
			open := m.Reply != "" && !strings.HasSuffix(m.Reply, "\n")
			if open && term.IsTerminal(int(os.Stdout.Fd())) {
				fmt.Println()
				open = false
			}
			if open && (len(saved) > 0 || (save == "" && slices.ContainsFunc(m.Files, func(f store.File) bool { return f.Reply }))) {
				fmt.Fprintln(os.Stderr)
			}
			if save == "" {
				noteFiles(m.ID, m.Files, true)
			}
			for _, s := range saved {
				if s.URI != "" {
					fmt.Fprintf(os.Stderr, "link: %s → %s\n", s.Name, s.URI)
				} else {
					fmt.Fprintf(os.Stderr, "saved: %s\n", s.Path)
				}
			}
		}
		if saveErr != nil {
			return saveErr
		}
		switch {
		case !m.State.Final():
			pending = append(pending, m.ID)
		case m.State != store.Done:
			why := m.Error
			if why == "" {
				why = string(m.State)
			}
			code := m.ErrorCode
			if code == "" {
				code = string(m.State)
			}
			failed = append(failed, fmt.Sprintf("%s %s [%s]: %s", m.ID, m.State, code, why))
			last = m
		}
	}
	if len(failed) > 0 {
		// One failure: its code and hint; several: each line has its code.
		e := &exitErr{code: exitFailed, err: errors.New(strings.Join(failed, "\n")), name: "failed"}
		if len(failed) == 1 {
			e.name, e.hint = last.ErrorCode, last.Hint
			if e.name == "" {
				e.name = string(last.State)
			}
		}
		return e
	}
	if len(pending) > 0 {
		ids := strings.Join(pending, " ")
		return &exitErr{code: exitTimeout, name: "timeout", err: fmt.Errorf("timed out waiting for %s; it keeps going", ids),
			hint: "`acpost wait " + ids + "` continues waiting; `acpost cancel " + pending[0] + "` stops it"}
	}
	return nil
}

func waitCmd(g *globals) *cobra.Command {
	var timeout time.Duration
	var save string
	cmd := &cobra.Command{
		Use:   "wait ID... [--save DIR]",
		Short: "Wait for messages to be answered and print the replies",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, ids []string) error {
			if err := nonNegative(map[string]time.Duration{"--timeout": timeout}); err != nil {
				return err
			}
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			return waitAndPrint(cmd.Context(), g, c, ids, timeout, save)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long (exit 4); 0: no limit")
	cmd.Flags().StringVar(&save, "save", "", "save the files attached to the replies into this directory")
	return cmd
}

func showCmd(g *globals) *cobra.Command {
	var events, follow bool
	cmd := &cobra.Command{
		Use:   "show ID",
		Short: "Show a message",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := g.connect(ctx)
			if err != nil {
				return err
			}
			if follow {
				return followMessage(ctx, g, c, args[0])
			}
			m, err := c.Get(ctx, args[0])
			if err != nil {
				return err
			}
			var evs []store.Event
			if events {
				for after := int64(0); ; {
					page, err := c.Events(ctx, m.ID, after, store.MaxPage)
					if err != nil {
						return err
					}
					if len(page) == 0 {
						break
					}
					evs = append(evs, page...)
					after = page[len(page)-1].Seq
				}
			}
			if g.json {
				if events {
					printJSON(struct {
						store.Message
						Events []store.Event `json:"events"`
					}{m, evs})
				} else {
					printJSON(m)
				}
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			row := func(k, v string) {
				if v != "" {
					fmt.Fprintf(w, "%s:\t%s\n", k, v)
				}
			}
			row("id", m.ID)
			row("state", string(m.State))
			row("from", m.From)
			row("to", m.To)
			row("task", m.Task)
			row("thread", m.Thread)
			row("parent", m.Parent)
			row("attempts", fmt.Sprint(m.Attempts))
			row("created", m.CreatedAt.Local().Format(time.DateTime))
			if !m.FinishedAt.IsZero() {
				row("finished", m.FinishedAt.Local().Format(time.DateTime))
			}
			row("error", m.Error)
			for _, f := range m.Files {
				k := "file"
				if f.Reply {
					k = "reply file"
				}
				row(k, describe(f))
			}
			w.Flush()
			fmt.Printf("\n%s\n", strings.TrimRight(m.Body, "\n"))
			if m.Reply != "" {
				fmt.Printf("\n--- reply\n%s\n", strings.TrimRight(m.Reply, "\n"))
			}
			if len(m.Files) > 0 {
				fmt.Printf("\n`acpost files %s` saves the message's files, `--reply` the reply's\n", m.ID)
			}
			if events {
				fmt.Println("\n--- events")
				for _, e := range evs {
					fmt.Printf("%s %-16s %s\n", e.Time.Local().Format(time.TimeOnly), e.Kind, e.Data)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&events, "events", false, "also show what the agent did (tool calls, permissions)")
	cmd.Flags().BoolVar(&follow, "follow", false, "show it as it runs: the agent's text as it writes, what it does, then the outcome")
	return cmd
}

func listCmd(g *globals) *cobra.Command {
	var f client.ListFilter
	cmd := &cobra.Command{
		Use:         "list",
		Annotations: quick,
		Short:       "List messages, oldest first (a page; --after continues)",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			p, err := c.List(cmd.Context(), f)
			if err != nil {
				return err
			}
			if g.json {
				for _, m := range p.Items {
					printJSON(m)
				}
				if p.Next != "" {
					printJSON(map[string]string{"next": p.Next})
				}
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, m := range p.Items {
				fmt.Fprintf(w, "%s\t%s\t%s → %s\t%s\t%s\n", m.ID, m.State, m.From, m.To, m.Task, m.CreatedAt.Local().Format(time.DateTime))
			}
			w.Flush()
			if p.Next != "" {
				fmt.Fprintf(os.Stderr, "more: --after %s\n", p.Next)
			}
			return nil
		},
	}
	fl := cmd.Flags()
	fl.StringVar(&f.To, "to", "", "recipient")
	fl.StringVar(&f.From, "from", "", "sender")
	fl.StringVar(&f.Task, "task", "", "task")
	fl.StringVar(&f.Thread, "thread", "", "thread")
	fl.StringVar(&f.State, "state", "", "queued, running, done, failed, cancelled, expired")
	fl.IntVar(&f.Limit, "limit", 50, "messages per page (max 500)")
	fl.StringVar(&f.After, "after", "", "continue after this cursor")
	return cmd
}

func cancelCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:         "cancel ID",
		Annotations: quick,
		Short:       "Cancel a message that is not answered yet",
		Args:        cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			m, err := c.Cancel(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if g.json {
				printJSON(m)
			} else {
				fmt.Println(m.ID, m.State)
			}
			return nil
		},
	}
}

func recvCmd(g *globals) *cobra.Command {
	var as string
	var wait, lease time.Duration
	cmd := &cobra.Command{
		Use:   "recv --as NAME",
		Short: "Take the next message for an inbox agent (answer it with reply)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if as == "" {
				return usageErr(errors.New("--as is required"))
			}
			if err := nonNegative(map[string]time.Duration{"--wait": wait, "--lease": lease}); err != nil {
				return err
			}
			ctx := cmd.Context()
			c, err := g.connect(ctx)
			if err != nil {
				return err
			}
			deadline := time.Now().Add(wait)
			for {
				step := max(min(time.Until(deadline), client.MaxWait), 0)
				// The long poll itself is bounded: a node that stalls does not hold recv past it.
				cctx, done := context.WithTimeout(ctx, step+quickTimeout)
				m, ok, err := c.Claim(cctx, strings.TrimPrefix(as, "agent:"), step, lease)
				done()
				if err != nil {
					return err
				}
				if ok {
					if g.json {
						printJSON(m)
					} else {
						fmt.Printf("id: %s\nlease: %s\nfrom: %s\n", m.ID, m.LeaseID, m.From)
						if m.Task != "" {
							fmt.Printf("task: %s\n", m.Task)
						}
						for _, f := range m.Files {
							fmt.Printf("file: %s\n", describe(f))
						}
						fmt.Printf("\n%s", m.Body)
						if len(m.Files) > 0 {
							fmt.Fprintf(os.Stderr, "\n`acpost files %s` saves the files\n", m.ID)
						}
					}
					return nil
				}
				if !time.Now().Before(deadline) {
					return &exitErr{code: exitTimeout, err: errors.New("no message"), hint: "nothing is queued for this agent; --wait 30s waits for one"}
				}
			}
		},
	}
	cmd.Flags().StringVar(&as, "as", "", "the inbox agent to take a message for")
	cmd.Flags().DurationVar(&wait, "wait", 0, "wait this long for a message (exit 4 if none)")
	cmd.Flags().DurationVar(&lease, "lease", 0, "how long the message is yours to answer (default: the agent's lease)")
	return cmd
}

func replyCmd(g *globals) *cobra.Command {
	var lease, body, bodyFile, fail string
	var attach, links []string
	cmd := &cobra.Command{
		Use:   "reply ID --lease L (--body TEXT | --body-file F | stdin | --fail REASON) [--attach FILE]...",
		Short: "Answer a message taken with recv",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if lease == "" {
				return usageErr(errors.New("--lease is required (recv prints it)"))
			}
			r := client.ReplyRequest{Lease: lease, Error: fail}
			if fail == "" {
				text, ok, err := readBody(cmd, body, bodyFile)
				if err != nil {
					return err
				}
				if !ok && len(attach) == 0 && len(links) == 0 {
					return usageErr(errors.New("no reply: --body, --body-file, stdin, --attach, --link or --fail"))
				}
				r.Body = text
			}
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			files, err := readFiles(cmd.Context(), c, attach)
			if err != nil {
				return err
			}
			linked, err := readLinks(links)
			if err != nil {
				return err
			}
			r.Files = append(files, linked...)
			rctx, done := context.WithTimeout(cmd.Context(), requestBudget(r.Body, r.Files))
			defer done()
			return c.Reply(rctx, args[0], r)
		},
	}
	f := cmd.Flags()
	f.StringVar(&lease, "lease", "", "the lease recv gave")
	f.StringVar(&body, "body", "", "the reply")
	f.StringVar(&bodyFile, "body-file", "", "read the reply from a file (- for stdin)")
	f.StringArrayVarP(&attach, "attach", "a", nil, "attach a file to the reply (repeatable)")
	f.StringArrayVar(&links, "link", nil, "attach a link to a file of this machine or an http(s) URL, not a copy (repeatable)")
	f.StringVar(&fail, "fail", "", "end the message as failed, with this reason")
	return cmd
}

func agentsCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:         "agents",
		Annotations: quick,
		Short:       "List the agents the node knows",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			list, err := c.Agents(cmd.Context())
			if err != nil {
				return err
			}
			if g.json {
				printJSON(list)
				return nil
			}
			return printAgents(list)
		},
	}
}

func printAgents(list []client.AgentInfo) error {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "AGENT\tDRIVER\tQUEUED\tRUNNING\tPROCESS")
	for _, a := range list {
		proc := ""
		if a.Process {
			proc = "up"
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\t%s\n", a.Address, a.Driver, a.Queued, a.Running, proc)
	}
	return w.Flush()
}

func sessionsCmd(g *globals) *cobra.Command {
	var agent, cwd, cursor string
	cmd := &cobra.Command{
		Use:         "sessions --agent NAME",
		Annotations: quick,
		Short:       "List an ACP agent's sessions, newest first (for send --session)",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if agent == "" {
				return usageErr(errors.New("--agent is required"))
			}
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			list, next, err := c.Sessions(cmd.Context(), strings.TrimPrefix(agent, "agent:"), cwd, cursor)
			if err != nil {
				return err
			}
			if g.json {
				for _, si := range list {
					printJSON(si)
				}
				if next != "" {
					printJSON(map[string]string{"next": next})
				}
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tUPDATED\tCWD\tTITLE")
			for _, si := range list {
				updated := si.UpdatedAt
				if t, err := time.Parse(time.RFC3339Nano, si.UpdatedAt); err == nil {
					updated = t.Local().Format(time.DateTime)
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", si.ID, updated, si.Cwd, cut(si.Title, 61)) // by runes: UTF-8 stays whole
			}
			w.Flush()
			if next != "" {
				fmt.Fprintf(os.Stderr, "more: --cursor %s\n", next)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&agent, "agent", "", "the agent")
	cmd.Flags().StringVar(&cwd, "cwd", "", "only sessions of this working directory")
	cmd.Flags().StringVar(&cursor, "cursor", "", "continue a long list")
	return cmd
}

func statusCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:         "status",
		Annotations: quick,
		Short:       "Check the node is up",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// Like every command, status starts the local node when there is none: it is the first
			// call an agent makes, and it should not tell a story the next command contradicts.
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			// connect asked already (the local node): not asked twice.
			s := client.Status{}
			if g.status != nil {
				s = *g.status
			} else if s, err = c.Status(cmd.Context()); err != nil {
				return err
			}
			// One call tells an agent all it needs to start: the node, who it is, whom it can write to.
			agents, err := c.Agents(cmd.Context())
			if err != nil {
				return err
			}
			if g.json {
				printJSON(struct {
					client.Status
					Agents []client.AgentInfo `json:"agents"`
				}{s, agents})
				return nil
			}
			fmt.Printf("ok: node %s, acpost %s, you are %s", s.Node, s.Version, s.You)
			if s.Hub != "" {
				fmt.Printf(", joined %s", s.Hub)
			}
			fmt.Println()
			return printAgents(agents)
		},
	}
}

// requestBudget is how long a request carrying a body and files may take: quickTimeout, and a
// second more for each 256 KiB it carries (a slow link uploads; a stuck node is still found).
func requestBudget(body string, files []store.File) time.Duration {
	n := len(body)
	for _, f := range files {
		n += len(f.Data)
	}
	return quickTimeout + time.Duration(n/(256<<10))*time.Second
}

// nonNegative refuses a negative duration flag: -5s is a typo, not "no limit" (what 0 says).
func nonNegative(flags map[string]time.Duration) error {
	for name, d := range flags {
		if d < 0 {
			return usageErr(fmt.Errorf("%s %s: a duration of 0 or more", name, d))
		}
	}
	return nil
}

// nodeURL is the node commands talk to: --node / ACPOST_NODE, else the local node's own address
// from its configuration (cfg, then, is that configuration).
func (g *globals) nodeURL() (string, *config.Config, error) {
	if g.node != "" {
		return g.node, nil, nil
	}
	cfg, err := g.loadConfig()
	if err != nil {
		return "", nil, usageErr(err)
	}
	return localURL(cfg), cfg, nil
}

// loadConfig reads the configuration: a file named by --config or ACPOST_CONFIG must exist (a
// typo is an error, not a node on defaults); only the implicit default may be absent.
func (g *globals) loadConfig() (*config.Config, error) {
	if g.config != "" {
		return config.LoadNamed(g.configPath())
	}
	return config.Load(g.configPath())
}
