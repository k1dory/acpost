package main

import (
	"errors"
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/k1dory/acpx"
	"github.com/spf13/cobra"
)

func approveCmd(g *globals) *cobra.Command {
	var option string
	var reject, list bool
	cmd := &cobra.Command{
		Use:   "approve [ID [REQUEST]]",
		Short: "Answer an agent's request for permission (permissions: ask): allow once, --option, or --reject",
		Args:  cobra.MaximumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			c, err := g.connect(ctx)
			if err != nil {
				return err
			}
			if list || len(args) == 0 {
				items, err := c.Asking(ctx)
				if err != nil {
					return err
				}
				if g.json {
					printJSON(items)
					return nil
				}
				w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				for _, it := range items {
					opts := ""
					for _, o := range it.Options {
						opts += o.ID + "(" + o.Kind + ") "
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", it.Message, it.ID, it.Title, opts)
				}
				return w.Flush()
			}
			id := args[0]
			req := ""
			if len(args) == 2 {
				req = args[1]
			}
			if req != "" && (option != "" || reject) { // no listing needed (a sender's token may not list)
				reply := acpx.Reply{RequestID: req, OptionID: option}
				if reject {
					reply = acpx.Reply{RequestID: req, Action: acpx.ActionDecline}
				}
				return c.Answer(ctx, id, reply)
			}
			items, err := c.Asking(ctx)
			if err != nil {
				return err
			}
			var found *acpx.Request
			for i := range items {
				if items[i].Message == id && (req == "" || items[i].ID == req) {
					if found != nil && req == "" {
						return usageErr(errors.New("several requests wait on " + id + ": name one (acpost approve --list)"))
					}
					found = &items[i].Request
				}
			}
			if found == nil {
				return &exitErr{code: exitNotFound, name: "not_found", err: fmt.Errorf("no permission request waits on %s", id)}
			}
			reply := acpx.Reply{RequestID: found.ID}
			switch {
			case reject:
				reply.Action = acpx.ActionDecline
			case option != "":
				reply.OptionID = option
			default: // allow once: the option of that kind
				for _, o := range found.Options {
					if o.Kind == "allow_once" {
						reply.OptionID = o.ID
					}
				}
				if reply.OptionID == "" {
					return usageErr(errors.New("the request offers no allow_once: --option one of its options"))
				}
			}
			return c.Answer(ctx, id, reply)
		},
	}
	cmd.Flags().StringVar(&option, "option", "", "the option to pick (its id)")
	cmd.Flags().BoolVar(&reject, "reject", false, "refuse")
	cmd.Flags().BoolVar(&list, "list", false, "list the requests waiting")
	return cmd
}
