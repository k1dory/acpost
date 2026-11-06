package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/k1dory/acpost/internal/client"
)

func usageCmd(g *globals) *cobra.Command {
	var f client.UsageFilter
	var since time.Duration
	var from, to string
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "What agents' turns used and cost: tokens, cache, cost, by agent, model, day or session",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx := cmd.Context()
			var err error
			if f.From, err = day(from); err != nil {
				return usageErr(fmt.Errorf("--from: %w", err))
			}
			if f.To, err = day(to); err != nil {
				return usageErr(fmt.Errorf("--to: %w", err))
			}
			if f.From.IsZero() && since > 0 {
				f.From = time.Now().Add(-since)
			}
			c, err := g.connect(ctx)
			if err != nil {
				return err
			}
			items, err := c.Usage(ctx, f)
			if err != nil {
				return err
			}
			if g.json {
				for _, it := range items {
					printJSON(it)
				}
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', tabwriter.AlignRight)
			key := f.By
			if key == "" {
				key = "all"
			}
			fmt.Fprintf(w, "%s\tturns\tinput\toutput\tcache read\tcache write\tcache %%\tcost\t\n", key)
			for _, it := range items {
				k := it.Key
				if k == "" {
					k = "all"
				}
				fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\t%.0f%%\t%s\t\n", k, it.Turns, tokens(it.Input), tokens(it.Output),
					tokens(it.CacheRead), tokens(it.CacheWrite), it.CacheRatio*100, cost(it))
			}
			w.Flush()
			for _, it := range items {
				if it.UnpricedTurns > 0 {
					fmt.Fprintf(os.Stderr, "· %s: %d turns without a cost and no price for %v (prices: in the node's config)\n", it.Key, it.UnpricedTurns, it.Unpriced)
				}
			}
			return nil
		},
	}
	cmd.Flags().DurationVar(&since, "since", 30*24*time.Hour, "this long back (unless --from)")
	cmd.Flags().StringVar(&from, "from", "", "from this day or time (2026-11-01, RFC 3339)")
	cmd.Flags().StringVar(&to, "to", "", "up to this day or time, not including it")
	cmd.Flags().StringVar(&f.By, "by", "agent", "group by agent, model, day, session, or \"\" for one total")
	cmd.Flags().StringVar(&f.Agent, "agent", "", "only this agent (agent:name@node)")
	return cmd
}

// day reads a day (local) or an RFC 3339 time; "" is none.
func day(s string) (time.Time, error) {
	if s == "" {
		return time.Time{}, nil
	}
	if t, err := time.ParseInLocation(time.DateOnly, s, time.Local); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339, s)
}

func tokens(n int64) string {
	switch {
	case n >= 1e9:
		return fmt.Sprintf("%.1fG", float64(n)/1e9)
	case n >= 1e6:
		return fmt.Sprintf("%.1fM", float64(n)/1e6)
	case n >= 1e4:
		return fmt.Sprintf("%.1fk", float64(n)/1e3)
	}
	return fmt.Sprint(n)
}

// cost is the agents' own cost and the estimate for turns without one (~).
func cost(r client.UsageReport) string {
	switch {
	case r.Costed > 0 && r.EstimatedTurns > 0 && r.Currency == r.EstimatedCurrency:
		return fmt.Sprintf("%.2f %s (~%.2f est.)", r.Cost+r.Estimated, r.Currency, r.Estimated)
	case r.Costed > 0 && r.EstimatedTurns > 0:
		return fmt.Sprintf("%.2f %s + ~%.2f %s", r.Cost, r.Currency, r.Estimated, r.EstimatedCurrency)
	case r.Costed > 0:
		return fmt.Sprintf("%.2f %s", r.Cost, r.Currency)
	case r.EstimatedTurns > 0:
		return fmt.Sprintf("~%.2f %s", r.Estimated, r.EstimatedCurrency)
	}
	return "-"
}
