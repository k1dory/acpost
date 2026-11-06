package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/k1dory/acpost/internal/store"
)

func changesCmd(g *globals) *cobra.Command {
	var tree, stat bool
	cmd := &cobra.Command{
		Use:   "changes ID",
		Short: "What a message's turns changed in their git repository (--tree: and its subagents')",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := g.connect(ctx)
			if err != nil {
				return err
			}
			ch, err := c.ChangesOf(ctx, args[0], tree, !stat)
			if err != nil {
				return err
			}
			if g.json {
				printJSON(ch)
				return nil
			}
			if len(ch.Turns) == 0 {
				fmt.Fprintln(os.Stderr, "· no changes recorded: the agent changed nothing, or works outside a git repository")
				return nil
			}
			if stat {
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				for _, t := range ch.Turns {
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", t.At.Local().Format("15:04:05"), t.Agent, filepath.Base(t.Repo), t.Branch)
					for _, f := range t.Files {
						fmt.Fprintf(w, "\t%s %s\t%s\t\n", f.Status, f.Path, lineStat(f))
					}
				}
				return w.Flush()
			}
			// The whole of each repository when it can be had (git keeps the snapshots a while),
			// else its turns one by one.
			whole := map[string]bool{}
			for _, r := range ch.Repos {
				whole[r.Repo] = true
				printDiff(r.Repo, r.Branch, r.Diff, r.Cut)
			}
			for _, t := range ch.Turns {
				if !whole[t.Repo] {
					printDiff(t.Repo+" ("+t.Agent+")", t.Branch, t.Diff, t.Cut)
				}
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&tree, "tree", false, "with the messages its agent sent while working on it (subagents)")
	cmd.Flags().BoolVar(&stat, "stat", false, "files and lines only")
	return cmd
}

func lineStat(f store.ChangedFile) string {
	if f.Added < 0 {
		return "binary"
	}
	return fmt.Sprintf("+%d -%d", f.Added, f.Removed)
}

func printDiff(repo, branch, diff string, cut bool) {
	head := "# " + repo
	if branch != "" {
		head += " @ " + branch
	}
	fmt.Fprintln(os.Stderr, head)
	fmt.Print(diff)
	if diff != "" && !strings.HasSuffix(diff, "\n") {
		fmt.Println()
	}
	if cut {
		fmt.Fprintln(os.Stderr, "· the diff was longer than kept (1 MiB)")
	}
}
