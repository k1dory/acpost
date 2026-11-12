// A force layout for the wiki's graph, small and our own: items push each other apart, edges pull
// their ends together, a weak pull keeps the whole near the centre, and the steps shrink as it
// settles. It starts from a seeded arrangement, so the same set is laid out the same way each time.

export type Point = { x: number; y: number };
export type LayoutOptions = {
  seed?: number;
  iterations?: number;
  /** The length an edge settles at. */
  distance?: number;
  /** Items that stay where they are (dragged ones). */
  pinned?: Map<string, Point>;
};

/** A seeded random number generator (mulberry32): numbers in [0, 1). */
export function seeded(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}

/**
 * Where each item goes. ids are taken in sorted order, so the result depends on the set and the
 * seed, not on the order they came in. Repulsion is between every pair (a few hundred items:
 * tens of thousands of pairs a step), beyond a cut-off distance it is left out.
 */
export function layout(ids: string[], edges: { from: string; to: string }[], opt: LayoutOptions = {}): Map<string, Point> {
  const order = [...new Set(ids)].sort();
  const n = order.length;
  const out = new Map<string, Point>();
  if (!n) return out;
  const rand = seeded(opt.seed ?? 1);
  const d = opt.distance ?? 70;
  const iterations = opt.iterations ?? (n > 300 ? 180 : 300);
  const index = new Map(order.map((id, i) => [id, i]));
  const x = new Float64Array(n), y = new Float64Array(n), fx = new Float64Array(n), fy = new Float64Array(n);
  const fixed = new Uint8Array(n);
  // A disc a little larger than the settled graph, filled at random.
  const radius = d * Math.sqrt(n) * 0.8;
  for (let i = 0; i < n; i++) {
    const p = opt.pinned?.get(order[i]);
    const a = rand() * Math.PI * 2, r = radius * Math.sqrt(rand());
    if (p) { x[i] = p.x; y[i] = p.y; fixed[i] = 1; }
    else { x[i] = Math.cos(a) * r; y[i] = Math.sin(a) * r; }
  }
  const links: [number, number][] = [];
  for (const e of edges) {
    const a = index.get(e.from), b = index.get(e.to);
    if (a !== undefined && b !== undefined && a !== b) links.push([a, b]);
  }
  links.sort((p, q) => p[0] - q[0] || p[1] - q[1]); // sums in one order: the same result to the last digit
  const k2 = d * d, cut2 = (d * 6) ** 2;
  let temp = radius / 4;
  const cool = Math.pow(0.5 / Math.max(temp, 1), 1 / iterations);
  for (let it = 0; it < iterations; it++) {
    fx.fill(0); fy.fill(0);
    for (let i = 0; i < n; i++) {
      for (let j = i + 1; j < n; j++) {
        let dx = x[i] - x[j], dy = y[i] - y[j];
        let q = dx * dx + dy * dy;
        if (q > cut2) continue;
        if (q < 0.01) { dx = (i - j) * 0.1; dy = 0.1; q = dx * dx + dy * dy; } // two at one place: apart, the same way each time
        const f = k2 / q;
        fx[i] += dx * f; fy[i] += dy * f;
        fx[j] -= dx * f; fy[j] -= dy * f;
      }
    }
    for (const [a, b] of links) {
      const dx = x[a] - x[b], dy = y[a] - y[b];
      const dist = Math.sqrt(dx * dx + dy * dy) || 0.01;
      const f = dist / d;
      fx[a] -= dx * f; fy[a] -= dy * f;
      fx[b] += dx * f; fy[b] += dy * f;
    }
    for (let i = 0; i < n; i++) {
      if (fixed[i]) continue;
      fx[i] -= x[i] * 0.02; fy[i] -= y[i] * 0.02; // towards the centre: parts apart do not drift off
      const len = Math.sqrt(fx[i] * fx[i] + fy[i] * fy[i]);
      if (len > 0) {
        const step = Math.min(len, temp) / len;
        x[i] += fx[i] * step; y[i] += fy[i] * step;
      }
    }
    temp *= cool;
  }
  for (let i = 0; i < n; i++) out.set(order[i], { x: Math.round(x[i] * 100) / 100, y: Math.round(y[i] * 100) / 100 });
  return out;
}
