<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { tilde } from "../lib/names";
  import { ago } from "../lib/format";
  import { linkTarget, neighbourhood, type Graph, type Item } from "../lib/wikigraph";
  import Icon from "./Icon.svelte";
  import Markdown from "./Markdown.svelte";
  import WikiGraph from "./WikiGraph.svelte";

  // One item of the wiki's set, at the side: its text (its links followed here), what it links
  // to (broken ones too), what links to it, and the graph around it.
  let { item, graph, onselect, onopen, onclose }: {
    item: Item;
    graph: Graph;
    onselect: (id: string) => void;
    onopen: (it: Item) => void;
    onclose: () => void;
  } = $props();

  const outs = $derived(graph.out.get(item.id) ?? []);
  const ins = $derived(graph.in.get(item.id) ?? []);
  const lost = $derived(graph.broken.filter(b => b.from === item.id));
  const near = $derived(neighbourhood(graph, item.id));
  const resolve = $derived((target: string) => graph.resolve(item, target));
  const where = (it: Item) => (it.kind === "page" ? tilde(it.repo ?? "", app.home) : it.scope ? tilde(it.scope, app.home) : t("everywhere"));
  const title = (id: string) => graph.byId.get(id)?.title ?? id;

  function follow(e: MouseEvent) {
    const a = (e.target as HTMLElement).closest("a");
    const h = a?.getAttribute("href") ?? "";
    if (h.startsWith("#wiki-broken:")) { e.preventDefault(); return; }
    const id = linkTarget(h);
    if (id && graph.byId.has(id)) { e.preventDefault(); onselect(id); }
  }
</script>

<svelte:window onkeydown={e => { if (e.key === "Escape") onclose(); }} />

<aside class="drawer" aria-label={item.title}>
  <header>
    <span class="chip">{t(item.kind)}</span>
    <div class="hd">
      <b class="ttl" title={item.title}>{item.title}</b>
      <span class="muted small mono" title={item.kind === "page" ? item.repo : item.scope}>{where(item)}{#if item.kind === "page"} · {item.key}{/if}</span>
    </div>
    <button class="btn sm" onclick={() => onopen(item)}>{t("Open")}</button>
    <button class="btn ghost icon" onclick={onclose} title={t("Close")} aria-label={t("Close")}><Icon name="x" size={13} /></button>
  </header>
  <div class="body">
    {#if item.updated}<div class="muted small">{t("Updated")} {ago(item.updated)}{#if item.kind === "note" && !item.enabled} · {t("off")}{/if}</div>{/if}
    <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
    <article onclick={follow}>
      {#if item.body.trim()}<Markdown text={item.body} wikiLink={resolve} />{:else}<div class="muted">{t("No text")}</div>{/if}
    </article>

    <h3>{t("Links out")} <span class="muted">{outs.length + lost.length}</span></h3>
    <ul>
      {#each outs as e (e.to + e.type)}
        <li><button class="lk" onclick={() => onselect(e.to)}>{title(e.to)}</button> <span class="muted small">{t(e.type)}</span></li>
      {/each}
      {#each lost as b (b.target)}
        <li><span class="broken">{b.target}</span> <span class="muted small">{t("broken")}</span></li>
      {/each}
      {#if !outs.length && !lost.length}<li class="muted small">{t("None")}</li>{/if}
    </ul>

    <h3>{t("Backlinks")} <span class="muted">{ins.length}</span></h3>
    <ul>
      {#each ins as e (e.from + e.type)}
        <li><button class="lk" onclick={() => onselect(e.from)}>{title(e.from)}</button> <span class="muted small">{t(e.type)}</span></li>
      {:else}
        <li class="muted small">{t("None")}</li>
      {/each}
    </ul>

    {#if near.ids.length > 1}
      <h3>{t("Neighbours")}</h3>
      <div class="ng"><WikiGraph ids={near.ids} items={graph.byId} edges={near.edges} selected={item.id} compact {onselect} /></div>
    {/if}
  </div>
</aside>

<style>
  .drawer { position: absolute; top: 0; right: 0; bottom: 0; width: min(440px, 92%); background: var(--card); border-left: 1px solid var(--line); box-shadow: var(--shadow); display: flex; flex-direction: column; z-index: 5; }
  header { display: flex; align-items: center; gap: 8px; padding: 10px 10px 10px 16px; border-bottom: 1px solid var(--line-soft); }
  .hd { flex: 1; min-width: 0; display: grid; gap: 1px; }
  .ttl { font-size: 14px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .small { font-size: 11.5px; }
  .mono { font-family: var(--mono); font-size: 10.5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .body { flex: 1; overflow: auto; padding: 12px 16px 30px; display: flex; flex-direction: column; gap: 8px; }
  article { padding: 4px 0 8px; border-bottom: 1px solid var(--line-soft); }
  article :global(a[href^="#wiki-broken:"]) { color: var(--crit); text-decoration: underline dotted; cursor: default; }
  h3 { font-size: 12px; font-weight: 600; margin: 8px 0 0; display: flex; gap: 6px; }
  ul { list-style: none; margin: 0; padding: 0; display: grid; gap: 2px; }
  li { font-size: 12.5px; display: flex; align-items: baseline; gap: 6px; }
  .lk { border: 0; background: transparent; padding: 2px 0; color: var(--accent); text-align: left; font-size: 12.5px; }
  .lk:hover { text-decoration: underline; }
  .broken { color: var(--crit); text-decoration: underline dotted; }
  .ng { height: 220px; flex: none; }
</style>
