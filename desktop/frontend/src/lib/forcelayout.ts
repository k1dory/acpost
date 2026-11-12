// A force layout for the wiki's graph, small and our own: items push each other apart, edges pull
// their ends together, a weak pull keeps the whole near the centre, and the steps shrink as it
// settles. It starts from a seeded arrangement, so the same set is laid out the same way each time.
// It runs a step at a time (startLayout), so a large set can be laid out between frames.

export type Point = { x: number; y: number };
export type LayoutOptions = {
  seed?: number;
  iterations?: number;
  /** The length an edge settles at. */
  distance?: number;
  /** Items that stay where they are (dragged ones). */
  pinned?: Map<string, Point>;
  /** Counts the pairs of items compared (the work done), when given. */
  stats?: { pairs: number };
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

/** The steps a set of n items is given: fewer as it grows (each step costs more). */
export function stepsFor(n: number): number {
  return n > 1000 ? 100 : n > 300 ? 180 : 300;
}

export type Layout = {
  /** Runs up to k more steps: true when the layout is done. */
  step: (k: number) => boolean;
  /** How far it is, 0 to 1. */
  progress: () => number;
  /** Where each item is now. */
  positions: () => Map<string, Point>;
};

/**
 * A layout to run a step at a time. ids are taken in sorted order, so the result depends on the set
 * and the seed, not on the order they came in nor on how the steps are split. Repulsion is between
 * items within a cut-off distance only, found through a grid of cells that size: each item is
 * compared with those in its cell and the eight around it, not with every other.
 */
export function startLayout(ids: string[], edges: { from: string; to: string }[], opt: LayoutOptions = {}): Layout {
  const order = [...new Set(ids)].sort();
  const n = order.length;
  const rand = seeded(opt.seed ?? 1);
  const d = opt.distance ?? 70;
  const iterations = opt.iterations ?? stepsFor(n);
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
  const k2 = d * d, cut = d * 3, cut2 = cut * cut;
  let temp = radius / 4;
  const cool = Math.pow(0.5 / Math.max(temp, 1), 1 / iterations);
  const stats = opt.stats;
  let done = 0;

  // The grid: items by cell, each cell's in index order (a fixed order of sums).
  const cellOf = (v: number) => Math.floor(v / cut);
  const key = (cx: number, cy: number) => (cx + 0x8000) * 0x10000 + (cy + 0x8000);
  const cx = new Int32Array(n), cy = new Int32Array(n);
  function repel() {
    const cells = new Map<number, number[]>();
    for (let i = 0; i < n; i++) {
      cx[i] = cellOf(x[i]); cy[i] = cellOf(y[i]);
      const k = key(cx[i], cy[i]);
      const c = cells.get(k);
      if (c) c.push(i); else cells.set(k, [i]);
    }
    let pairs = 0;
    for (let i = 0; i < n; i++) {
      for (let ox = -1; ox <= 1; ox++) for (let oy = -1; oy <= 1; oy++) {
        const c = cells.get(key(cx[i] + ox, cy[i] + oy));
        if (!c) continue;
        for (const j of c) {
          if (j <= i) continue;
          pairs++;
          let dx = x[i] - x[j], dy = y[i] - y[j];
          let q = dx * dx + dy * dy;
          if (q > cut2) continue;
          if (q < 0.01) { dx = (i - j) * 0.1; dy = 0.1; q = dx * dx + dy * dy; } // two at one place: apart, the same way each time
          const f = k2 / q;
          fx[i] += dx * f; fy[i] += dy * f;
          fx[j] -= dx * f; fy[j] -= dy * f;
        }
      }
    }
    if (stats) stats.pairs += pairs;
  }

  function once() {
    fx.fill(0); fy.fill(0);
    repel();
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

  return {
    step(k) {
      for (let s = 0; s < k && done < iterations && n; s++, done++) once();
      return done >= iterations || !n;
    },
    progress: () => (n ? done / iterations : 1),
    positions() {
      const out = new Map<string, Point>();
      for (let i = 0; i < n; i++) out.set(order[i], { x: Math.round(x[i] * 100) / 100, y: Math.round(y[i] * 100) / 100 });
      return out;
    },
  };
}

/** Where each item goes: the whole layout at once (see startLayout). */
export function layout(ids: string[], edges: { from: string; to: string }[], opt: LayoutOptions = {}): Map<string, Point> {
  const l = startLayout(ids, edges, opt);
  while (!l.step(1000));
  return l.positions();
}
