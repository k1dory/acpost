import { describe, it, expect } from "vitest";
import { layout, seeded } from "./forcelayout";

const ids = Array.from({ length: 40 }, (_, i) => "n" + i);
const edges = ids.slice(1).map((id, i) => ({ from: ids[Math.floor(i / 3)], to: id }));

describe("force layout", () => {
  it("is the same for the same set and seed, whatever the order", () => {
    const a = layout(ids, edges, { seed: 7 });
    const b = layout([...ids].reverse(), [...edges].reverse(), { seed: 7 });
    expect([...b.entries()].sort()).toEqual([...a.entries()].sort());
    const c = layout(ids, edges, { seed: 8 });
    expect(ids.some(id => c.get(id)!.x !== a.get(id)!.x)).toBe(true);
    expect(seeded(3)()).toBe(seeded(3)());
  });

  it("keeps pinned items where they are, and places every item somewhere finite and apart", () => {
    const pinned = new Map([["n0", { x: 500, y: -500 }]]);
    const p = layout(ids, edges, { seed: 1, pinned });
    expect(p.get("n0")).toEqual({ x: 500, y: -500 });
    expect(p.size).toBe(ids.length);
    for (const q of p.values()) expect(Number.isFinite(q.x) && Number.isFinite(q.y)).toBe(true);
    const pts = [...p.values()];
    let close = 0;
    for (let i = 0; i < pts.length; i++) for (let j = i + 1; j < pts.length; j++) if (Math.hypot(pts[i].x - pts[j].x, pts[i].y - pts[j].y) < 5) close++;
    expect(close).toBe(0);
  });

  it("brings joined items nearer than the rest, and lays out a few hundred quickly", () => {
    const p = layout(ids, edges, { seed: 2 });
    const dist = (a: string, b: string) => Math.hypot(p.get(a)!.x - p.get(b)!.x, p.get(a)!.y - p.get(b)!.y);
    const joined = edges.reduce((s, e) => s + dist(e.from, e.to), 0) / edges.length;
    let all = 0, k = 0;
    for (const a of ids) for (const b of ids) if (a < b) { all += dist(a, b); k++; }
    expect(joined).toBeLessThan(all / k);
    const many = Array.from({ length: 400 }, (_, i) => "m" + i);
    const t0 = performance.now();
    layout(many, many.slice(1).map((id, i) => ({ from: many[i >> 1], to: id })), { seed: 1 });
    expect(performance.now() - t0).toBeLessThan(5000);
    expect(layout([], []).size).toBe(0);
  });
});
