<script lang="ts">
  // A chart from a reply: drawn here as SVG (no library), coloured by the app's theme; a value is
  // shown on hover.
  import { t } from "../../lib/i18n.svelte";
  import { fmt, ticks, type Chart } from "../../lib/genui";
  let { c }: { c: Chart } = $props();

  const W = 560, H = 220, L = 48, R = 12, T = 10, B = 34;
  const pw = W - L - R, ph = H - T - B;
  const n = $derived(c.labels.length);
  const all = $derived(c.series.flatMap(s => s.values));
  const scale = $derived(ticks(Math.min(...all), Math.max(...all)));
  const lo = $derived(scale[0]), hi = $derived(scale[scale.length - 1]);
  // A place on the plot for any value: one the scale cannot place (never for a checked block) sits on
  // its floor rather than giving the drawing NaN.
  const y = (v: number) => {
    const at = T + ph - ((v - lo) / (hi - lo)) * ph;
    return Number.isFinite(at) ? at : T + ph;
  };
  // A category's band (bars) or point (lines): the middle of its share of the width.
  const band = $derived(pw / n);
  const x = (i: number) => L + band * i + band / 2;
  // Labels under the axis: as many as fit, evenly spread.
  const every = $derived(Math.max(1, Math.ceil(n / Math.floor(pw / 56))));
  const unit = $derived(c.unit ? " " + c.unit : "");
  const many = $derived(c.series.length > 1);

  let hover = $state<{ i: number; s?: number } | null>(null);
  const tip = $derived.by(() => {
    if (!hover) return "";
    const i = hover.i;
    if (c.kind === "pie") {
      const v = c.series[0].values[i], sum = c.series[0].values.reduce((a, b) => a + b, 0);
      return `${c.labels[i]}: ${fmt(v)}${unit} (${fmt((v / sum) * 100)}%)`;
    }
    const of = hover.s === undefined ? c.series.map((_, k) => k) : [hover.s];
    return c.labels[i] + ": " + of.map(k => (many ? c.series[k].name + " " : "") + fmt(c.series[k].values[i]) + unit).join(", ");
  });

  // Bars side by side within a category.
  const bw = $derived(Math.max(2, (band * 0.72) / c.series.length));
  const zero = $derived(y(Math.max(lo, Math.min(0, hi))));
  const path = (vals: number[]) => vals.map((v, i) => `${i ? "L" : "M"}${x(i).toFixed(1)},${y(v).toFixed(1)}`).join("");
  const area = (vals: number[]) => path(vals) + `L${x(vals.length - 1).toFixed(1)},${zero.toFixed(1)}L${x(0).toFixed(1)},${zero.toFixed(1)}Z`;

  // A pie's slices, from the top, clockwise.
  const slices = $derived.by(() => {
    if (c.kind !== "pie") return [];
    const vals = c.series[0].values, sum = vals.reduce((a, b) => a + b, 0);
    const cx = W / 2, cy = H / 2, r = H / 2 - 12;
    let a = -Math.PI / 2;
    return vals.map((v, i) => {
      const da = (v / sum) * Math.PI * 2, a2 = a + da;
      const p = (t: number) => `${(cx + r * Math.cos(t)).toFixed(2)},${(cy + r * Math.sin(t)).toFixed(2)}`;
      const d = da >= Math.PI * 2 - 1e-9
        ? `M${cx - r},${cy}a${r},${r} 0 1,0 ${2 * r},0a${r},${r} 0 1,0 ${-2 * r},0Z`
        : `M${cx},${cy}L${p(a)}A${r},${r} 0 ${da > Math.PI ? 1 : 0},1 ${p(a2)}Z`;
      a = a2;
      return { d, i, v };
    }).filter(s => s.v > 0);
  });
  const legend = $derived(c.kind === "pie" ? c.labels : many ? c.series.map(s => s.name) : []);
  const colour = (k: number) => `var(--s${(k % 8) + 1})`;
</script>

<div class="chart">
  <svg viewBox="0 0 {W} {H}" role="img" aria-label={c.title ?? t("Chart")} onmouseleave={() => hover = null}>
    {#if c.kind === "pie"}
      {#each slices as s (s.i)}
        <path class="slice" class:dim={hover && hover.i !== s.i} d={s.d} fill={colour(s.i)} onmouseenter={() => hover = { i: s.i }} role="presentation"><title>{c.labels[s.i]}: {fmt(s.v)}{unit}</title></path>
      {/each}
    {:else}
      {#each scale as v}
        <line class="grid" x1={L} x2={W - R} y1={y(v)} y2={y(v)} />
        <text class="ax" x={L - 6} y={y(v) + 3.5} text-anchor="end">{fmt(v)}</text>
      {/each}
      <line class="axis" x1={L} x2={W - R} y1={zero} y2={zero} />
      {#each c.labels as l, i}
        {#if i % every === 0}<text class="ax" x={x(i)} y={H - B + 16} text-anchor="middle">{l.length > 12 ? l.slice(0, 11) + "…" : l}</text>{/if}
      {/each}
      {#if c.kind === "bar"}
        {#each c.series as s, k}
          {#each s.values as v, i}
            <rect class="bar" class:dim={hover && (hover.i !== i || hover.s !== k)} x={x(i) - (bw * c.series.length) / 2 + bw * k} width={bw - 1}
              y={Math.min(y(v), zero)} height={Math.max(1, Math.abs(zero - y(v)))} rx="2" fill={colour(k)}
              onmouseenter={() => hover = { i, s: k }} role="presentation"><title>{c.labels[i]}: {fmt(v)}{unit}</title></rect>
          {/each}
        {/each}
      {:else}
        {#each c.series as s, k}
          {#if c.kind === "area"}<path d={area(s.values)} fill={colour(k)} fill-opacity="0.16" stroke="none" />{/if}
          <path d={path(s.values)} fill="none" stroke={colour(k)} stroke-width="2" stroke-linejoin="round" stroke-linecap="round" />
          {#each s.values as v, i}
            {#if n <= 30 || hover?.i === i}<circle cx={x(i)} cy={y(v)} r={hover?.i === i ? 4 : 2.5} fill={colour(k)} />{/if}
          {/each}
        {/each}
        {#if hover}<line class="cursor" x1={x(hover.i)} x2={x(hover.i)} y1={T} y2={T + ph} />{/if}
        <!-- A column a category: hovering anywhere in it shows its values. -->
        {#each c.labels as l, i}
          <rect class="hit" x={L + band * i} y={T} width={band} height={ph} onmouseenter={() => hover = { i }} role="presentation"><title>{l}</title></rect>
        {/each}
      {/if}
    {/if}
  </svg>
  <div class="tip" class:on={!!tip}>{tip || " "}</div>
  {#if legend.length}
    <div class="legend">
      {#each legend as name, k}<span><i style:background={colour(k)}></i>{name}</span>{/each}
    </div>
  {/if}
</div>

<style>
  .chart {
    --s1: var(--accent); --s2: var(--good); --s3: var(--warn); --s4: var(--crit);
    --s5: #8a5cd0; --s6: #1b9aaa; --s7: #c4568f; --s8: var(--muted);
  }
  :global([data-theme="dark"]) .chart { --s5: #b393ef; --s6: #4fc6d4; --s7: #e58bbb; }
  svg { width: 100%; height: auto; display: block; max-height: 260px; }
  .grid { stroke: var(--line-soft); }
  .axis { stroke: var(--line); }
  .ax { font: 10px var(--sans); fill: var(--muted); }
  .cursor { stroke: var(--line); stroke-dasharray: 3 3; }
  .hit { fill: transparent; }
  .bar, .slice { transition: opacity .12s; }
  .slice { stroke: var(--page); stroke-width: 1.5; }
  .dim { opacity: .45; }
  .tip { font-size: 12px; color: var(--ink-2); min-height: 18px; font-variant-numeric: tabular-nums; visibility: hidden; }
  .tip.on { visibility: visible; }
  .legend { display: flex; flex-wrap: wrap; gap: 4px 14px; font-size: 12px; color: var(--ink-2); }
  .legend span { display: inline-flex; align-items: center; gap: 6px; }
  .legend i { width: 10px; height: 10px; border-radius: 3px; flex: none; }
</style>
