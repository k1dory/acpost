package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"text/tabwriter"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/k1dory/acpost/internal/client"
)

func termCmd(g *globals) *cobra.Command {
	var r client.TermRequest
	var id string
	var list, keep bool
	cmd := &cobra.Command{
		Use:   "term",
		Short: "A shell on the node's machine, in an agent's working directory (--agent) or --cwd",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			c, err := g.connect(ctx)
			if err != nil {
				return err
			}
			if list {
				ts, err := c.Terminals(ctx)
				if err != nil {
					return err
				}
				if g.json {
					printJSON(ts)
					return nil
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				fmt.Fprintln(w, "ID\tCWD\tAGENT\tRUNNING")
				for _, t := range ts {
					fmt.Fprintf(w, "%s\t%s\t%s\t%v\n", t.ID, t.Cwd, t.Agent, t.Running)
				}
				return w.Flush()
			}
			in := int(os.Stdin.Fd())
			if !term.IsTerminal(in) {
				return usageErr(errors.New("acpost term needs a terminal (it is interactive)"))
			}
			cols, rows, _ := term.GetSize(in)
			if id == "" {
				r.Cols, r.Rows = cols, rows
				t, err := c.OpenTerminal(ctx, r)
				if err != nil {
					return err
				}
				id = t.ID
				fmt.Fprintf(os.Stderr, "· %s in %s (acpost term --id %s comes back to it)\r\n", t.ID, t.Cwd, t.ID)
			} else {
				_ = c.TermResize(ctx, id, cols, rows)
			}
			if !keep {
				defer c.CloseTerminal(context.Background(), id) // however this ends
			}
			old, err := term.MakeRaw(in)
			if err != nil {
				return err
			}
			defer term.Restore(in, old)
			ctx, cancel := context.WithCancel(ctx)
			defer cancel()
			stop := make(chan os.Signal, 1) // ended from outside: the same cleanup
			signal.Notify(stop, syscall.SIGTERM, syscall.SIGHUP)
			defer signal.Stop(stop)
			go func() {
				select {
				case <-stop:
					cancel()
				case <-ctx.Done():
				}
			}()
			// The window's size follows it.
			go onResize(ctx, func() {
				if w, h, err := term.GetSize(in); err == nil {
					_ = c.TermResize(ctx, id, w, h)
				}
			})
			go func() { // what is typed goes to the shell
				b := make([]byte, 4096)
				for {
					k, err := os.Stdin.Read(b)
					if k > 0 && c.TermInput(ctx, id, b[:k]) != nil {
						cancel()
						return
					}
					if err != nil {
						return
					}
				}
			}()
			t, err := c.TermOutput(ctx, id, 0, func(_ int64, b []byte) error {
				_, err := os.Stdout.Write(b)
				return err
			})
			if err != nil && !errors.Is(err, context.Canceled) {
				return err
			}
			if t.ExitCode != nil && *t.ExitCode != 0 {
				return &exitErr{code: exitFailed, name: "failed", err: fmt.Errorf("the shell exited with %d", *t.ExitCode)}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&r.Agent, "agent", "", "in this agent's working directory")
	cmd.Flags().StringVar(&r.Cwd, "cwd", "", "in this directory (of the node's machine)")
	cmd.Flags().StringVar(&id, "id", "", "come back to this terminal")
	cmd.Flags().BoolVar(&list, "list", false, "list the open terminals")
	cmd.Flags().BoolVar(&keep, "keep", false, "leave the terminal open when this ends")
	return cmd
}
