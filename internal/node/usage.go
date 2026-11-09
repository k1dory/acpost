package node

import (
	"context"
	"strings"

	"github.com/k1dory/acpost/internal/client"
	"github.com/k1dory/acpost/internal/store"
)

// Usage sums what agents' turns used and cost, grouped as q says: turns this node ran, and those
// whose events it holds (sent from here, or routed through it as a hub). Only the node's user and
// its agents may read it.
func (n *Node) Usage(ctx context.Context, p Principal, q store.UsageQuery) ([]client.UsageReport, error) {
	if !p.local() {
		return nil, errf("refused", "usage is for the node's user")
	}
	sums, err := n.st.UsageOf(ctx, q)
	if err != nil {
		return nil, invalidOr(err)
	}
	out := make([]client.UsageReport, 0, len(sums))
	for _, s := range sums {
		r := client.UsageReport{UsageSum: s}
		// Input is without the cache (claude-agent-acp, codex-acp): the share of what the model read
		// that came from the cache.
		if read := s.Input + s.CacheRead + s.CacheWrite; read > 0 {
			r.CacheRatio = float64(s.CacheRead) / float64(read)
		}
		for model, t := range s.Uncosted {
			price, ok := n.cfg.Prices[model]
			if !ok {
				r.Unpriced = append(r.Unpriced, model)
				r.UnpricedTurns += t.Turns
				continue
			}
			cur := price.Currency
			if cur == "" {
				cur = "USD"
			}
			r.Estimated += (float64(t.Input)*price.Input + float64(t.Output)*price.Output +
				float64(t.CacheRead)*price.CacheRead + float64(t.CacheWrite)*price.CacheWrite) / 1e6
			r.EstimatedTurns += t.Turns
			if r.EstimatedCurrency == "" {
				r.EstimatedCurrency = cur
			} else if !strings.EqualFold(r.EstimatedCurrency, cur) {
				r.EstimatedCurrency = "mixed"
			}
		}
		out = append(out, r)
	}
	return out, nil
}
