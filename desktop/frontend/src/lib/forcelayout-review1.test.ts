import { describe, it, expect } from "vitest";
import { layout, startLayout, stepsFor } from "./forcelayout";

// Review round 1: the layout at the binding's limit is bounded work, and a layout run in slices
// is the layout run at once.
describe("force layout at scale", () => {
  const set = (n: number) => {
    const ids = Array.from({ length: n }, (_, i) => "n" + i);
    return { ids, edges: ids.slice(1).map((id, i) => ({ from: ids[(i * 7) % (i + 1)], to: id })) };
  };

  it("compares nearby items only: 3000 items take a small part of the pairs of every item with every other", () => {
    const { ids, edges } = set(3000);
    const stats = { pairs: 0 };
    const t0 = performance.now();
    layout(ids, edges, { seed: 1, stats });
    const ms = performance.now() - t0;
    const all = (3000 * 2999) / 2 * stepsFor(3000);
    expect(stepsFor(3000)).toBeLessThanOrEqual(100);
    expect(stats.pairs).toBeGreaterThan(0);
    expect(stats.pairs).toBeLessThan(all / 10);
    expect(ms).toBeLessThan(20000);
  }, 60000);

  it("is the same whether run at once or in slices of any size", () => {
    const { ids, edges } = set(400);
    const whole = layout(ids, edges, { seed: 5 });
    const l = startLayout(ids, edges, { seed: 5 });
    let slices = 0;
    while (!l.step(7)) { slices++; expect(l.progress()).toBeLessThan(1); }
    expect(slices).toBeGreaterThan(5);
    expect(l.progress()).toBe(1);
    expect([...l.positions().entries()]).toEqual([...whole.entries()]);
    expect(layout([...ids].reverse(), edges, { seed: 5 })).toEqual(whole);
  });
});
