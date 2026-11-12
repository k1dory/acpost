<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node, errText } from "../lib/api";
  import { ago } from "../lib/format";
  import { buildGraph, matches, type Graph, type Item, type Kind } from "../lib/wikigraph";
  import Icon from "./Icon.svelte";
  import WikiGraph from "./WikiGraph.svelte";
  import WikiDrawer from "./WikiDrawer.svelte";

  // Everything the agents keep, at once: every repository's wiki pages and the knowledge notes,
  // as a table or as the graph of their links, over one loaded set. An item opens at the side.
  let { mode, selected = $bindable(""), onopen }: {
    mode: "table" | "graph";
    selected?: string;
    onopen: (it: Item) => void;
  } = $props();

  let graph = $state<Graph | null>(null);
  let more = $state(false);
  let loading = $state(false);
  let err = $state("");
  let q = $state("");
  let kind = $state<"" | Kind>("");
  type Col = "title" | "kind" | "where" | "out" | "in" | "updated";
  let sort = $state<{ col: Col; desc: boolean }>({ col: "updated", desc: true });

  // Both lists asked together; an answer to an older refresh is dropped.
  let seq = 0;
  async function load() {
    const my = ++seq;
    loading = true; err = "";
    try {
      const [all, notes] = await Promise.all([Node.AllWikis(), Node.Knowledge()]);
      if (my !== seq) return;
      graph = buildGraph((all?.pages ?? []) as any, (notes ?? []) as any);
      more = !!all?.more;
      if (selected && !graph.byId.has(selected)) selected = "";
    } catch (e) { if (my === seq) err = errText(e); }
    finally { if (my === seq) loading = false; }
  }
  $effect(() => { load(); });

  const base = (d: string) => d.split("/").filter(Boolean).pop() || d;
  const where = (it: Item) => (it.kind === "page" ? base(it.repo ?? "") : it.scope ? base(it.scope) : t("everywhere"));
  const time = (s: string) => Date.parse(s) || 0;

  const shown = $derived.by(() => (graph?.items ?? []).filter(it => (!kind || it.kind === kind) && matches(it, q)));
  const rows = $derived.by(() => {
    const g = graph;
    if (!g) return [];
    const val = (it: Item): string | number => {
      switch (sort.col) {
        case "title": return it.title.toLowerCase();
        case "kind": return it.kind;
        case "where": return where(it).toLowerCase();
        case "out": return g.out.get(it.id)!.length;
        case "in": return g.in.get(it.id)!.length;
        case "updated": return time(it.updated);
      }
    };
    const dir = sort.desc ? -1 : 1;
    return [...shown].sort((a, b) => {
      const x = val(a), y = val(b);
      return (x < y ? -dir : x > y ? dir : 0) || a.title.localeCompare(b.title);
    });
  });
  function by(col: Col) {
    sort = sort.col === col ? { col, desc: !sort.desc } : { col, desc: col === "updated" || col === "out" || col === "in" };
  }
  // The graph is laid out over the whole set (a search does not move it); what is not shown fades.
  const ids = $derived((graph?.items ?? []).map(it => it.id));
  const on = $derived(new Set(shown.map(it => it.id)));
  const faded = $derived(on.size === ids.length ? undefined : new Set(ids.filter(id => !on.has(id))));
  const lonely = $derived((graph?.lonely ?? []).filter(it => on.has(it.id)));
  const broken = $derived((graph?.broken ?? []).filter(b => on.has(b.from)));
  const item = $derived(selected ? graph?.byId.get(selected) : undefined);
  const cols: [Col, string][] = [["title", "Title"], ["kind", "Kind"], ["where", "Repository or scope"], ["out", "Links out"], ["in", "Links in"], ["updated", "Updated"]];
</script>

<div class="ex">
  <div class="bar">
    <div class="search"><Icon name="search" size={12} /><input class="input" placeholder={t("Search titles and text")} bind:value={q} /></div>
    <div class="seg">
      <button class:on={kind === ""} onclick={() => (kind = "")}>{t("All")}</button>
      <button class:on={kind === "page"} onclick={() => (kind = "page")}>{t("Pages")}</button>
      <button class:on={kind === "note"} onclick={() => (kind = "note")}>{t("Notes")}</button>
    </div>
    <span class="muted small">{t("{n} shown", { n: shown.length })}{#if graph} · {t("{n} links", { n: graph.edges.length })} · {t("{n} broken", { n: graph.broken.length })}{/if}</span>
    <span class="sp"></span>
    <button class="btn sm" onclick={load} disabled={loading}>{#if loading}<span class="spin"></span>{:else}<Icon name="refresh" size={12} />{/if} {t("Refresh")}</button>
  </div>
  {#if err}<div class="err selectable">{err}</div>{/if}
  {#if more}<div class="warn small">{t("Not every page is loaded: there are more than the app reads at once.")}</div>{/if}

  <div class="area">
    {#if !graph}
      <div class="empty muted">{#if loading}<span class="spin"></span>{/if} {t("Loading…")}</div>
    {:else if !graph.items.length}
      <div class="empty muted">{t("Nothing yet: write a wiki for a repository, or add knowledge in Settings.")}</div>
    {:else if mode === "table"}
      <div class="tw">
        <table>
          <thead><tr>
            {#each cols as [c, label] (c)}
              <th class:num={c === "out" || c === "in"} aria-sort={sort.col === c ? (sort.desc ? "descending" : "ascending") : "none"}>
                <button onclick={() => by(c)}>{t(label)}{#if sort.col === c} <span class="ar">{sort.desc ? "↓" : "↑"}</span>{/if}</button>
              </th>
            {/each}
          </tr></thead>
          <tbody>
            {#each rows as it (it.id)}
              <tr class:on={it.id === selected} onclick={() => (selected = it.id)} onkeydown={e => { if (e.key === "Enter") selected = it.id; }} tabindex="0">
                <td class="tt">{it.title}</td>
                <td><span class="kd {it.kind}">{t(it.kind)}</span></td>
                <td class="muted" title={it.kind === "page" ? it.repo : it.scope}>{where(it)}</td>
                <td class="num">{graph.out.get(it.id)!.length}</td>
                <td class="num">{graph.in.get(it.id)!.length}</td>
                <td class="muted">{it.updated ? ago(it.updated) : ""}</td>
              </tr>
            {:else}
              <tr><td colspan="6" class="muted">{t("Nothing matches")}</td></tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else}
      <div class="gcol">
        <div class="canvas"><WikiGraph {ids} items={graph.byId} edges={graph.edges} {faded} {selected} onselect={id => (selected = id)} /></div>
        <div class="lists">
          <div>
            <h3>{t("Unconnected")} <span class="muted">{lonely.length}</span></h3>
            {#each lonely as it (it.id)}<button class="lk" onclick={() => (selected = it.id)}>{it.title}</button>{:else}<span class="muted small">{t("None")}</span>{/each}
          </div>
          <div>
            <h3>{t("Broken links")} <span class="muted">{broken.length}</span></h3>
            {#each broken as b (b.from + "\n" + b.target)}
              <div class="bl"><button class="lk" onclick={() => (selected = b.from)}>{graph.byId.get(b.from)?.title}</button> → <span class="broken">{b.target}</span></div>
            {:else}<span class="muted small">{t("None")}</span>{/each}
          </div>
        </div>
      </div>
    {/if}
    {#if item && graph}
      <WikiDrawer {item} {graph} onselect={id => (selected = id)} {onopen} onclose={() => (selected = "")} />
    {/if}
  </div>
</div>

<style>
  .ex { flex: 1; min-height: 0; display: flex; flex-direction: column; }
  .bar { display: flex; align-items: center; gap: 8px; padding: 10px 20px; border-bottom: 1px solid var(--line-soft); flex-wrap: wrap; }
  .search { display: flex; align-items: center; gap: 6px; color: var(--muted); }
  .search .input { height: 28px; width: 240px; font-size: 12.5px; }
  .sp { flex: 1; }
  .small { font-size: 11.5px; }
  .area { position: relative; flex: 1; min-height: 0; display: flex; }
  .empty { margin: auto; display: flex; align-items: center; gap: 8px; padding: 40px; text-align: center; max-width: 440px; }
  .tw { flex: 1; overflow: auto; }
  table { width: 100%; border-collapse: collapse; font-size: 12.5px; }
  th { position: sticky; top: 0; background: var(--page); text-align: left; border-bottom: 1px solid var(--line); padding: 0; z-index: 1; }
  th button { border: 0; background: transparent; width: 100%; text-align: inherit; padding: 8px 12px; font-size: 11px; font-weight: 600; color: var(--muted); text-transform: uppercase; }
  th button:hover { color: var(--ink); }
  th.num, td.num { text-align: right; width: 80px; }
  td { padding: 7px 12px; border-bottom: 1px solid var(--line-soft); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; max-width: 320px; }
  td.tt { color: var(--ink); font-weight: 500; }
  tr { cursor: pointer; }
  tbody tr:hover { background: var(--hover); }
  tbody tr.on { background: var(--sel); }
  .kd { font-size: 11px; padding: 1px 7px; border-radius: 8px; }
  .kd.page { color: var(--accent); background: var(--accent-bg); }
  .kd.note { color: var(--good-text); background: var(--good-bg); }
  .ar { font-size: 10px; }
  .gcol { flex: 1; min-width: 0; display: flex; flex-direction: column; }
  .canvas { flex: 1; min-height: 240px; }
  .lists { flex: none; max-height: 34%; overflow: auto; display: grid; grid-template-columns: 1fr 1fr; gap: 16px; padding: 10px 20px 14px; border-top: 1px solid var(--line-soft); }
  .lists > div { display: flex; flex-direction: column; align-items: flex-start; gap: 2px; min-width: 0; }
  h3 { font-size: 12px; font-weight: 600; margin: 0 0 4px; display: flex; gap: 6px; }
  .lk { border: 0; background: transparent; padding: 1px 0; color: var(--accent); text-align: left; font-size: 12.5px; }
  .lk:hover { text-decoration: underline; }
  .bl { font-size: 12.5px; color: var(--muted); }
  .broken { color: var(--crit); text-decoration: underline dotted; }
  .err { padding: 10px 12px; margin: 10px 20px 0; border-radius: 12px; background: var(--crit-bg); color: var(--crit); font-size: 12.5px; }
  .warn { padding: 6px 20px; color: var(--warn); background: var(--warn-bg); }
</style>
