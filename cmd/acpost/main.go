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
	"path/filepath"
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
	exitOK       = 0
	exitError    = 1
	exitUsage    = 2
	exitNotFound = 3
	exitTimeout  = 4
	exitFailed   = 5
	exitNoNode   = 6
	exitRefused  = 7
)

type exitErr struct {
	code int
	err  error
}

func (e *exitErr) Error() string { return e.err.Error() }
func (e *exitErr) Unwrap() error { return e.err }

func usageErr(err error) error { return &exitErr{exitUsage, err} }

type globals struct {
	node, token, config string
	json                bool
	explicitNode        bool
}

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
	root := rootCmd(g)
	err := root.ExecuteContext(context.Background())
	if err == nil {
		return
	}
	code := exitCode(err)
	if g.json {
		e := map[string]any{"code": codeName(err), "message": err.Error()}
		b, _ := json.Marshal(map[string]any{"error": e})
		fmt.Fprintln(os.Stderr, string(b))
	} else {
		fmt.Fprintln(os.Stderr, "acpost:", err)
	}
	os.Exit(code)
}

func exitCode(err error) int {
	var ee *exitErr
	if errors.As(err, &ee) {
		return ee.code
	}
	var ce *client.Error
	if errors.As(err, &ce) {
		switch ce.Code {
		case "invalid":
			return exitUsage
		case "not_found":
			return exitNotFound
		case "refused":
			return exitRefused
		case "timeout":
			return exitTimeout
		case "unavailable":
			return exitNoNode
		}
		return exitError
	}
	if errors.Is(err, client.ErrUnavailable) {
		return exitNoNode
	}
	if isFlagErr(err) {
		return exitUsage
	}
	return exitError
}

func codeName(err error) string {
	var ce *client.Error
	if errors.As(err, &ce) {
		return ce.Code
	}
	switch exitCode(err) {
	case exitUsage:
		return "invalid"
	case exitNotFound:
		return "not_found"
	case exitTimeout:
		return "timeout"
	case exitFailed:
		return "failed"
	case exitNoNode:
		return "unavailable"
	case exitRefused:
		return "refused"
	}
	return "error"
}

func isFlagErr(err error) bool {
	s := err.Error()
	return strings.HasPrefix(s, "unknown flag") || strings.HasPrefix(s, "unknown shorthand") || strings.Contains(s, "flag needs an argument") ||
		strings.HasPrefix(s, "invalid argument") || strings.HasPrefix(s, "required flag") || strings.Contains(s, "accepts ") ||
		strings.HasPrefix(s, "unknown command") || strings.Contains(s, "requires at least")
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
			if !g.json {
				g.json = os.Getenv("ACPOST_JSON") == "1"
			}
			if g.config == "" {
				g.config = os.Getenv("ACPOST_CONFIG")
			}
			return nil
		},
	}
	root.SetFlagErrorFunc(func(_ *cobra.Command, err error) error { return usageErr(err) })
	pf := root.PersistentFlags()
	pf.StringVar(&g.node, "node", "", "node to talk to: unix:///path, http(s)://host:port (ACPOST_NODE; default: the local node)")
	pf.StringVar(&g.token, "token", "", "bearer token (ACPOST_TOKEN)")
	pf.BoolVar(&g.json, "json", false, "JSON output (ACPOST_JSON=1)")
	pf.StringVar(&g.config, "config", "", "configuration file (ACPOST_CONFIG; default ~/.config/acpost/config.yaml)")

	msgCmds := func() []*cobra.Command {
		return []*cobra.Command{sendCmd(g), waitCmd(g), showCmd(g), listCmd(g), cancelCmd(g), recvCmd(g), replyCmd(g)}
	}
	root.AddCommand(msgCmds()...)
	message := &cobra.Command{Use: "message", Short: "The message commands again (acpost message send … = acpost send …)"}
	message.AddCommand(msgCmds()...)
	root.AddCommand(message, agentsCmd(g), statusCmd(g), serveCmd(g))
	return root
}

// connect returns a client for the node, starting the local node first if it is not running and
// none was named explicitly.
func (g *globals) connect(ctx context.Context) (*client.Client, error) {
	url := g.node
	var cfg *config.Config
	if url == "" {
		var err error
		cfg, err = config.Load(g.configPath())
		if err != nil {
			return nil, usageErr(err)
		}
		url = localURL(cfg)
	}
	c, err := client.New(url, g.token, "")
	if err != nil {
		return nil, usageErr(err)
	}
	if g.explicitNode || cfg == nil || !strings.HasPrefix(url, "unix://") {
		return c, nil
	}
	if _, err := c.Status(ctx); err == nil || !errors.Is(err, client.ErrUnavailable) {
		return c, nil
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
	self, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(self, "serve", "--config", g.configPath())
	cmd.Stdout, cmd.Stderr = logf, logf
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	cmd.Dir = "/"
	if err := cmd.Start(); err != nil {
		return &exitErr{exitNoNode, fmt.Errorf("start the local node: %w", err)}
	}
	exited := make(chan struct{})
	go func() { _ = cmd.Wait(); close(exited) }()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.Status(ctx); err == nil {
			_ = cmd.Process.Release()
			return nil
		}
		select {
		case <-exited:
			// Another command may have started a node at the same moment: it holds the store.
			if _, err := c.Status(ctx); err == nil {
				return nil
			}
			time.Sleep(200 * time.Millisecond)
			if _, err := c.Status(ctx); err == nil {
				return nil
			}
			return &exitErr{exitNoNode, fmt.Errorf("the local node did not start: see %s", filepath.Join(dir, "acpost.log"))}
		case <-time.After(50 * time.Millisecond):
		}
	}
	return &exitErr{exitNoNode, fmt.Errorf("the local node did not answer within 15s: see %s", filepath.Join(dir, "acpost.log"))}
}

func printJSON(v any) {
	b, _ := json.Marshal(v)
	fmt.Println(string(b))
}

// readBody takes the body from --body, --body-file or stdin (when it is not a terminal).
func readBody(cmd *cobra.Command, body, file string) (string, bool, error) {
	switch {
	case cmd.Flags().Changed("body"):
		return body, true, nil
	case file == "-":
		b, err := io.ReadAll(os.Stdin)
		return string(b), true, err
	case file != "":
		b, err := os.ReadFile(file)
		return string(b), true, err
	case !term.IsTerminal(int(os.Stdin.Fd())):
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false, err
		}
		return string(b), len(b) > 0, nil
	}
	return "", false, nil
}

func sendCmd(g *globals) *cobra.Command {
	var to, body, bodyFile, task, thread, idem, session string
	var meta []string
	var ttl, timeout time.Duration
	var wait bool
	cmd := &cobra.Command{
		Use:   "send --to ADDR [--body TEXT | --body-file F | stdin]",
		Short: "Send a message; prints its id",
		Example: `  acpost send --to agent:reviewer --task TASK-42 --body "Review PR #123"
  git diff | acpost send --to reviewer --wait --timeout 10m`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if to == "" {
				return usageErr(errors.New("--to is required"))
			}
			text, ok, err := readBody(cmd, body, bodyFile)
			if err != nil {
				return err
			}
			if !ok {
				return usageErr(errors.New("no body: --body, --body-file or stdin"))
			}
			req := client.SendRequest{To: to, Body: text, Task: task, Thread: thread, Session: session}
			for _, kv := range meta {
				k, v, found := strings.Cut(kv, "=")
				if !found || k == "" {
					return usageErr(fmt.Errorf("--meta %q: k=v", kv))
				}
				if req.Meta == nil {
					req.Meta = map[string]string{}
				}
				req.Meta[k] = v
			}
			if ttl > 0 {
				req.TTL = ttl.String()
			}
			ctx := cmd.Context()
			c, err := g.connect(ctx)
			if err != nil {
				return err
			}
			m, _, err := c.Send(ctx, req, idem)
			if err != nil {
				return err
			}
			if wait {
				return waitAndPrint(ctx, g, c, []string{m.ID}, timeout)
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
	f.StringVar(&task, "task", "", "task label; messages of one task share the agent's session")
	f.StringVar(&thread, "thread", "", "thread id")
	f.StringVar(&session, "session", "", "continue this existing ACP session of the agent (an editor's, say)")
	f.StringArrayVar(&meta, "meta", nil, "metadata k=v (repeatable)")
	f.DurationVar(&ttl, "ttl", 0, "expire if no agent took it within this long")
	f.StringVar(&idem, "idempotency-key", "", "a repeat with the same key returns the first message")
	f.BoolVar(&wait, "wait", false, "wait for the reply and print it")
	f.DurationVar(&timeout, "timeout", 0, "with --wait: give up waiting after this long (exit 4)")
	return cmd
}

// waitAndPrint waits for each message to be final and prints the replies.
func waitAndPrint(ctx context.Context, g *globals, c *client.Client, ids []string, timeout time.Duration) error {
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}
	var failed []string
	timedOut := false
	for i, id := range ids {
		left := time.Duration(0)
		if !deadline.IsZero() {
			left = max(time.Until(deadline), time.Millisecond)
		}
		m, err := c.WaitFinal(ctx, id, left)
		if err != nil {
			return err
		}
		if g.json {
			printJSON(m)
		} else {
			if len(ids) > 1 {
				if i > 0 && !strings.HasSuffix(m.Reply, "\n") {
					fmt.Println()
				}
				fmt.Println("--- " + m.ID)
			}
			fmt.Print(m.Reply)
			// A terminal gets a final newline; a pipe gets the reply exactly as it is.
			if m.Reply != "" && !strings.HasSuffix(m.Reply, "\n") && term.IsTerminal(int(os.Stdout.Fd())) {
				fmt.Println()
			}
		}
		switch {
		case !m.State.Final():
			timedOut = true
		case m.State != store.Done:
			why := m.Error
			if why == "" {
				why = string(m.State)
			}
			failed = append(failed, fmt.Sprintf("%s: %s: %s", m.ID, m.State, why))
		}
	}
	if len(failed) > 0 {
		return &exitErr{exitFailed, errors.New(strings.Join(failed, "; "))}
	}
	if timedOut {
		return &exitErr{exitTimeout, errors.New("timed out waiting; the message keeps going")}
	}
	return nil
}

func waitCmd(g *globals) *cobra.Command {
	var timeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait ID...",
		Short: "Wait for messages to be answered and print the replies",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, ids []string) error {
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			return waitAndPrint(cmd.Context(), g, c, ids, timeout)
		},
	}
	cmd.Flags().DurationVar(&timeout, "timeout", 0, "give up after this long (exit 4); 0: no limit")
	return cmd
}

func showCmd(g *globals) *cobra.Command {
	var events bool
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
			w.Flush()
			fmt.Printf("\n%s\n", strings.TrimRight(m.Body, "\n"))
			if m.Reply != "" {
				fmt.Printf("\n--- reply\n%s\n", strings.TrimRight(m.Reply, "\n"))
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
	return cmd
}

func listCmd(g *globals) *cobra.Command {
	var f client.ListFilter
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List messages, oldest first (a page; --after continues)",
		Args:  cobra.NoArgs,
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
		Use:   "cancel ID",
		Short: "Cancel a message that is not answered yet",
		Args:  cobra.ExactArgs(1),
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
			ctx := cmd.Context()
			c, err := g.connect(ctx)
			if err != nil {
				return err
			}
			deadline := time.Now().Add(wait)
			for {
				m, ok, err := c.Claim(ctx, strings.TrimPrefix(as, "agent:"), max(min(time.Until(deadline), client.MaxWait), 0), lease)
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
						fmt.Printf("\n%s", m.Body)
					}
					return nil
				}
				if !time.Now().Before(deadline) {
					return &exitErr{exitTimeout, errors.New("no message")}
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
	cmd := &cobra.Command{
		Use:   "reply ID --lease L (--body TEXT | --body-file F | stdin | --fail REASON)",
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
				if !ok {
					return usageErr(errors.New("no reply: --body, --body-file, stdin or --fail"))
				}
				r.Body = text
			}
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			return c.Reply(cmd.Context(), args[0], r)
		},
	}
	f := cmd.Flags()
	f.StringVar(&lease, "lease", "", "the lease recv gave")
	f.StringVar(&body, "body", "", "the reply")
	f.StringVar(&bodyFile, "body-file", "", "read the reply from a file (- for stdin)")
	f.StringVar(&fail, "fail", "", "end the message as failed, with this reason")
	return cmd
}

func agentsCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "agents",
		Short: "List the agents the node knows",
		Args:  cobra.NoArgs,
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
		},
	}
}

func statusCmd(g *globals) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Check the node is up",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			url := g.node
			if url == "" {
				cfg, err := config.Load(g.configPath())
				if err != nil {
					return usageErr(err)
				}
				url = localURL(cfg)
			}
			// status reports; it does not start a node.
			c, err := client.New(url, g.token, "")
			if err != nil {
				return usageErr(err)
			}
			s, err := c.Status(cmd.Context())
			if err != nil {
				return err
			}
			if g.json {
				printJSON(s)
			} else {
				fmt.Printf("ok: node %s, acpost %s, you are %s", s.Node, s.Version, s.You)
				if s.Hub != "" {
					fmt.Printf(", joined %s", s.Hub)
				}
				fmt.Println()
			}
			return nil
		},
	}
}
