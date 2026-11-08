// Usage rows summed. Costs are summed per currency: a total of dollars and euros is not one number.
import { money } from "./format";

export type UsageRow = {
  key: string; turns: number; input: number; output: number; cache_read: number; cache_write: number; total?: number;
  cost?: number; currency?: string; costed?: boolean; estimated?: number; estimated_currency?: string; cache_ratio?: number;
};

export type UsageTotal = { turns: number; input: number; output: number; cr: number; cw: number; costs: Record<string, number> };

export function usageTotal(rows: UsageRow[]): UsageTotal {
  const out: UsageTotal = { turns: 0, input: 0, output: 0, cr: 0, cw: 0, costs: {} };
  const addCost = (v: number | undefined, cur: string | undefined) => {
    if (!v) return;
    const c = cur || "USD";
    out.costs[c] = (out.costs[c] ?? 0) + v;
  };
  for (const r of rows) {
    out.turns += r.turns; out.input += r.input; out.output += r.output; out.cr += r.cache_read; out.cw += r.cache_write;
    addCost(r.cost, r.currency);
    addCost(r.estimated, r.estimated_currency || r.currency);
  }
  return out;
}

/** Costs said: one amount per currency, the largest first ("$0.00" when there are none). */
export function costText(costs: Record<string, number>): string {
  const all = Object.entries(costs).sort((a, b) => b[1] - a[1]);
  if (!all.length) return money(0, "USD");
  return all.map(([c, v]) => money(v, c)).join(" + ");
}
