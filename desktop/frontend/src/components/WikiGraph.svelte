<script lang="ts">
  import { untrack } from "svelte";
  import { t } from "../lib/i18n.svelte";
  import { startLayout, type Point } from "../lib/forcelayout";
  import type { Edge, Item } from "../lib/wikigraph";

  // The graph of what the agents keep, drawn as SVG: laid out once per set (seeded, so the same
  // each time), then zoomed with ⌘/ctrl and the wheel or a pinch, panned by dragging the
  // background or by scrolling, and a node dragged stays where it is put.
  let { ids, items, edges, faded, selected = "", compact = false, onselect }: {
    ids: string[];
    items: Map<string, Item>;
    edges: Edge[];
    /** Items drawn faint (not matching a search). */
    faded?: Set<string>;
    selected?: string;
    compact?: boolean;
    onselect: (id: string) => void;
  } = $props();

  let w = $state(600), h = $state(400);
  let svg: SVGSVGElement;
  // Where a dragged node was put: kept over a new layout of the same set.
  let moved = $state(new Map<string, Point>());
  // The layout of the set, made off the render path: steps for about 12 ms at a time, the window
  // the person's between them (a few hundred items are done in the first slice). Until it is done
  // the graph says so. A drag does not lay the set out again: where nodes were put is read, not
  // followed.
  let base = $state.raw(new Map<string, Point>());
  let laying = $state(0); // how far a layout under way is (0: none)
  $effect(() => {
    const run = startLayout(ids, edges, { seed: 1, distance: compact ? 60 : 70, pinned: untrack(() => moved) });
    let stop = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    const slice = () => {
      const t0 = performance.now();
      let done = run.step(1);
      while (!done && performance.now() - t0 < 12) done = run.step(1);
      if (done) { laying = 0; base = run.positions(); return; }
      laying = Math.max(run.progress(), 0.01);
      timer = setTimeout(() => { if (!stop) slice(); }, 0);
    };
    slice();
    return () => { stop = true; clearTimeout(timer); };
  });
  const pos = (id: string): Point => moved.get(id) ?? base.get(id) ?? { x: 0, y: 0 };

  // The view: x' = x·k + tx. Fitted to the set when it changes, not when a node is dragged.
  let k = $state(1), tx = $state(0), ty = $state(0);
  let fittedFor: Map<string, Point> | null = null;
  $effect(() => { if (base !== fittedFor && w > 0 && h > 0) { fittedFor = base; fit(); } });
  function fit() {
    let x0 = Infinity, y0 = Infinity, x1 = -Infinity, y1 = -Infinity;
    for (const id of ids) { const p = pos(id); x0 = Math.min(x0, p.x); y0 = Math.min(y0, p.y); x1 = Math.max(x1, p.x); y1 = Math.max(y1, p.y); }
    if (!isFinite(x0)) { k = 1; tx = w / 2; ty = h / 2; return; }
    const pad = compact ? 30 : 60;
    k = Math.min(2, Math.max(0.1, Math.min((w - pad * 2) / Math.max(x1 - x0, 1), (h - pad * 2) / Math.max(y1 - y0, 1))));
    tx = w / 2 - ((x0 + x1) / 2) * k;
    ty = h / 2 - ((y0 + y1) / 2) * k;
  }

  // The selected node and those it joins: labelled whatever the zoom.
  const near = $derived.by(() => {
    const s = new Set<string>();
    if (!selected) return s;
    s.add(selected);
    for (const e of edges) { if (e.from === selected) s.add(e.to); if (e.to === selected) s.add(e.from); }
    return s;
  });
  const shown = $derived(laying ? [] : ids.filter(id => items.has(id)));
  const lines = $derived(laying ? [] : edges.filter(e => items.has(e.from) && items.has(e.to)));
  const labels = $derived(compact || k >= 1.1);

  function zoomAt(f: number, cx: number, cy: number) {
    const nk = Math.min(4, Math.max(0.08, k * f));
    tx = cx - (cx - tx) * (nk / k);
    ty = cy - (cy - ty) * (nk / k);
    k = nk;
  }
  // The wheel: with ⌘ or ctrl (a pinch on a trackpad comes so), a zoom at the pointer; else a pan.
  // A listener of our own: Svelte's could be passive, and the page must not scroll instead.
  $effect(() => {
    const onWheel = (e: WheelEvent) => {
      e.preventDefault();
      const r = svg.getBoundingClientRect();
      if (e.ctrlKey || e.metaKey) zoomAt(Math.exp(-e.deltaY * 0.01), e.clientX - r.left, e.clientY - r.top);
      else { tx -= e.deltaX; ty -= e.deltaY; }
    };
    svg.addEventListener("wheel", onWheel, { passive: false });
    return () => svg.removeEventListener("wheel", onWheel);
  });

  // Dragging: a node (moved and pinned; a press with no move is a click on it), or the background.
  let drag: { id: string; x: number; y: number; px: number; py: number; moved: boolean } | null = null;
  function down(e: PointerEvent, id = "") {
    if (e.button !== 0) return;
    e.stopPropagation();
    (e.currentTarget as Element).setPointerCapture?.(e.pointerId);
    const p = id ? pos(id) : { x: tx, y: ty };
    drag = { id, x: p.x, y: p.y, px: e.clientX, py: e.clientY, moved: false };
  }
  function move(e: PointerEvent) {
    if (!drag) return;
    const dx = e.clientX - drag.px, dy = e.clientY - drag.py;
    if (!drag.moved && Math.abs(dx) + Math.abs(dy) < 3) return;
    drag.moved = true;
    if (!drag.id) { tx = drag.x + dx; ty = drag.y + dy; return; }
    const p = { x: drag.x + dx / k, y: drag.y + dy / k };
    moved = new Map(moved).set(drag.id, p);
  }
  function up() {
    if (drag?.id && !drag.moved) onselect(drag.id);
    else if (drag && !drag.id && !drag.moved && !compact) onselect("");
    drag = null;
  }
  function key(e: KeyboardEvent, id: string) {
    if (e.key === "Enter" || e.key === " ") { e.preventDefault(); onselect(id); }
  }
  // A label cut to fit beside its node.
  const short = (s: string) => (s.length > 28 ? s.slice(0, 27) + "…" : s);
</script>

<div class="gw" class:compact bind:clientWidth={w} bind:clientHeight={h}>
  <svg bind:this={svg} width={w} height={h} role="img" aria-label={t("Graph")}
    onpointerdown={e => down(e)} onpointermove={move} onpointerup={up} onpointercancel={() => (drag = null)}>
    <defs>
      <marker id={compact ? "wg-arrow-c" : "wg-arrow"} viewBox="0 0 10 10" refX="16" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse">
        <path d="M0 0L10 5L0 10z" class="arrow" />
      </marker>
    </defs>
    <g transform="translate({tx} {ty}) scale({k})">
      {#each lines as e (e.from + "\n" + e.to + "\n" + e.type)}
        {@const a = pos(e.from)}
        {@const b = pos(e.to)}
        {@const hot = e.from === selected || e.to === selected}
        <line x1={a.x} y1={a.y} x2={b.x} y2={b.y} class="edge" class:part={e.type === "part of"} class:hot class:faint={faded?.has(e.from) || faded?.has(e.to)}
          stroke-width={1 / Math.sqrt(k)} marker-end="url(#{compact ? 'wg-arrow-c' : 'wg-arrow'})" />
        {#if labels || hot}
          <text x={(a.x + b.x) / 2} y={(a.y + b.y) / 2 - 3} class="elabel" font-size={10 / Math.max(k, 0.6)}>{t(e.type)}</text>
        {/if}
      {/each}
      {#each shown as id (id)}
        {@const it = items.get(id)!}
        {@const p = pos(id)}
        <!-- svelte-ignore a11y_no_static_element_interactions -->
        <g class="node {it.kind}" class:sel={id === selected} class:pinned={moved.has(id)} class:faint={faded?.has(id)} transform="translate({p.x} {p.y})"
          onpointerdown={e => down(e, id)} onkeydown={e => key(e, id)} tabindex="0" role="button" aria-label={it.title}>
          <circle r={id === selected ? 8 : 6} />
          {#if labels || near.has(id) || k >= 0.7}
            <text x="10" y="4" font-size={11 / Math.max(k, 0.6)}>{short(it.title)}</text>
          {/if}
        </g>
      {/each}
    </g>
  </svg>
  {#if laying}<div class="laying muted"><span class="spin"></span> {t("Laying out… {n}%", { n: Math.round(laying * 100) })}</div>{/if}
  {#if !compact}
    <div class="tools">
      <button class="btn sm" onclick={() => zoomAt(1.25, w / 2, h / 2)} title={t("Zoom in")}>+</button>
      <button class="btn sm" onclick={() => zoomAt(0.8, w / 2, h / 2)} title={t("Zoom out")}>−</button>
      <button class="btn sm" onclick={fit}>{t("Fit")}</button>
      {#if moved.size}<button class="btn sm" onclick={() => (moved = new Map())}>{t("Unpin all")}</button>{/if}
    </div>
    <div class="legend muted">
      <span><i class="dot page"></i>{t("page")}</span>
      <span><i class="dot note"></i>{t("note")}</span>
      <span><i class="ln"></i>{t("link")}</span>
      <span><i class="ln part"></i>{t("part of")}</span>
      <span>{t("⌘ + scroll to zoom, drag to move")}</span>
    </div>
  {/if}
</div>

<style>
  .gw { position: relative; width: 100%; height: 100%; min-height: 200px; overflow: hidden; background: var(--page); }
  .gw.compact { min-height: 0; border: 1px solid var(--line-soft); border-radius: 14px; }
  svg { display: block; cursor: grab; touch-action: none; user-select: none; }
  svg:active { cursor: grabbing; }
  .edge { stroke: var(--line); }
  .edge.part { stroke-dasharray: 4 3; }
  .edge.hot { stroke: var(--ink-2); }
  .faint { opacity: .15; }
  .arrow { fill: var(--muted); }
  .elabel { fill: var(--muted); text-anchor: middle; pointer-events: none; }
  .node { cursor: pointer; outline: none; }
  .node circle { stroke: var(--page); stroke-width: 2; }
  .node.page circle { fill: var(--accent); }
  .node.note circle { fill: var(--good); }
  .node.pinned circle { stroke: var(--ink-2); }
  .node.sel circle { stroke: var(--ink); stroke-width: 2.5; }
  .node:focus-visible circle { stroke: var(--accent); stroke-width: 3; }
  .node text { fill: var(--ink-2); pointer-events: none; paint-order: stroke; stroke: var(--page); stroke-width: 3px; }
  .node.sel text { fill: var(--ink); font-weight: 600; }
  .laying { position: absolute; inset: 0; display: flex; align-items: center; justify-content: center; gap: 8px; font-size: 12.5px; pointer-events: none; }
  .tools { position: absolute; top: 10px; right: 10px; display: flex; gap: 4px; }
  .legend { position: absolute; left: 12px; bottom: 8px; display: flex; gap: 12px; font-size: 11px; align-items: center; flex-wrap: wrap; pointer-events: none; }
  .legend span { display: inline-flex; align-items: center; gap: 5px; }
  .dot { width: 9px; height: 9px; border-radius: 50%; display: inline-block; }
  .dot.page { background: var(--accent); }
  .dot.note { background: var(--good); }
  .ln { width: 18px; border-top: 1.5px solid var(--muted); display: inline-block; }
  .ln.part { border-top-style: dashed; }
</style>
