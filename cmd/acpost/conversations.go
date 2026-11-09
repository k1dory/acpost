package main

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
)

func conversationsCmd(g *globals) *cobra.Command {
	var before string
	var limit int
	cmd := &cobra.Command{
		Use:         "conversations",
		Annotations: quick,
		Aliases:     []string{"convs"},
		Short:       "Conversations (threads), the most recently active first",
		Args:        cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := g.connect(cmd.Context())
			if err != nil {
				return err
			}
			items, next, err := c.Conversations(cmd.Context(), before, limit)
			if err != nil {
				return err
			}
			if g.json {
				for _, it := range items {
					printJSON(it)
				}
			} else {
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				for _, it := range items {
					state := ""
					switch {
					case it.Running > 0:
						state = "running"
					case it.Queued > 0:
						state = "queued"
					case it.Failed > 0:
						state = "failed"
					}
					fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\t%s\n", it.Thread, it.Updated.Local().Format(time.DateTime), it.Messages, state,
						strings.Join(it.Agents, ","), cut(it.Title, 60))
				}
				w.Flush()
			}
			if next != "" {
				fmt.Fprintf(os.Stderr, "more: --before %s\n", next)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&before, "before", "", "the page after this cursor")
	cmd.Flags().IntVar(&limit, "limit", 50, "conversations a page")
	return cmd
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}
