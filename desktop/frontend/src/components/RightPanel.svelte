<script lang="ts">
  import { screenScale } from "../lib/format";
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import Icon from "./Icon.svelte";
  import Activity from "./Activity.svelte";
  import Changes from "./Changes.svelte";
  import Terminal from "./Terminal.svelte";
  import Forge from "./Forge.svelte";
  import Preview from "./Preview.svelte";
  import Artifacts from "./Artifacts.svelte";

  // Hidden (another view, or the panel closed) it stays mounted: the terminal's shell goes on.
  let { hidden = false }: { hidden?: boolean } = $props();
  let width = $state(420);
  try { width = +(localStorage.getItem("panel") ?? 420) || 420; } catch {}
  import type { PanelTab } from "../lib/state.svelte";
  // The views the panel can show; the tabs are those opened (the + opens one, × closes it).
  const views: Record<PanelTab, { label: string; icon: string; title?: string }> = {
    activity: { label: "Activity", icon: "activity" },
    changes: { label: "Changes", icon: "diff" },
    terminal: { label: "Terminal", icon: "term" },
    prs: { label: "PRs", icon: "pr", title: "Pull requests and CI" },
    preview: { label: "Preview", icon: "eye" },
    artifacts: { label: "Artifacts", icon: "file" },
  };
  const order = Object.keys(views) as PanelTab[];
  let adding = $state(false);
  let plusEl = $state<HTMLButtonElement>();
  let addEl = $state<HTMLDivElement>();
  let menuEl = $state<HTMLDivElement>();
  let menuPos = $state("visibility:hidden");
  // Under the +, kept inside the window (in the page's coordinates: the text size zoom out).
  $effect(() => {
    if (!adding || !plusEl || !menuEl) { menuPos = "visibility:hidden"; return; }
    const k = screenScale(), r = plusEl.getBoundingClientRect(), m = menuEl.getBoundingClientRect();
    const left = Math.max(8, Math.min(r.left / k, window.innerWidth / k - m.width / k - 8));
    menuPos = `left:${left}px;top:${r.bottom / k + 4}px`;
  });
  function pick(k: PanelTab) { app.openTab(k); adding = false; }
  const asks = $derived((app.rev, Object.values(app.turns).reduce((a, t) => a + t.waiting().length, 0)));

  function drag(e: PointerEvent) {
    const x0 = e.clientX, w0 = width, k = screenScale(); // the mouse moves in screen pixels
    const move = (ev: PointerEvent) => (width = Math.max(340, Math.min(760, w0 - (ev.clientX - x0) / k)));
    const up = () => { removeEventListener("pointermove", move); removeEventListener("pointerup", up); try { localStorage.setItem("panel", String(width)); } catch {} };
    addEventListener("pointermove", move); addEventListener("pointerup", up);
  }
</script>

<svelte:window onclick={e => { if (adding && !addEl?.contains(e.target as Node)) adding = false; }}
  onkeydown={e => { if (e.key === "Escape") adding = false; }} onresize={() => (adding = false)} />

<aside class="rp" class:hidden style="width:{width}px">
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="grip" onpointerdown={drag}></div>
  <div class="tabs drag">
    {#each app.tabs as k (k)}
      {@const v = views[k]}
      <div class="tab nodrag" class:on={app.tab === k}>
        <button class="tl" onclick={() => app.showTab(k)} title={t(v.title ?? v.label)}><Icon name={v.icon} size={14} />{t(v.label)}{#if k === "activity" && asks}<span class="badge">{asks}</span>{/if}</button>
        <button class="x" onclick={() => app.closeTab(k)} aria-label={t("Close")} title={t("Close")}><Icon name="x" size={10} /></button>
      </div>
    {/each}
    <div class="add nodrag" bind:this={addEl}>
      <button class="plus" bind:this={plusEl} class:on={adding} onclick={e => { e.stopPropagation(); adding = !adding; }} title={t("Open a view")} aria-label={t("Open a view")}><Icon name="plus" size={14} /></button>
      {#if adding}
        <div class="menu card" bind:this={menuEl} style={menuPos}>
          {#each order as k (k)}
            <button class="mi" onclick={() => pick(k)}>
              <Icon name={views[k].icon} size={13} /><span>{t(views[k].title ?? views[k].label)}</span>
              {#if app.tabs.includes(k)}<span class="ck"><Icon name="check" size={12} /></span>{/if}
            </button>
          {/each}
        </div>
      {/if}
    </div>
  </div>
  <div class="card panel body"><div class="inner">
    {#if hidden}<!-- hidden: nothing but the terminal stays (no fetching for a panel nobody sees) -->
    {:else if !app.tabs.length}<div class="empty">{t("Nothing open here. The + opens activity, changes, a terminal, pull requests or the preview.")}</div>
    {:else if app.tab === "activity"}<Activity />
    {:else if app.tab === "changes"}<Changes />
    {:else if app.tab === "prs"}<Forge />
    {:else if app.tab === "artifacts"}<Artifacts />
    {:else if app.tab === "preview"}<Preview covered={adding || app.palette || !!app.error} />{/if}
    <!-- The terminal, while its tab is open, stays mounted (hidden behind the others): its shell
         and what it printed survive switching tabs, other views, the panel closed. -->
    {#if app.tabs.includes("terminal")}<div class="pane" class:off={app.tab !== "terminal" || !app.tabs.length}><Terminal active={!hidden && app.tab === "terminal"} /></div>{/if}
  </div></div>
</aside>

<style>
  .rp { position: relative; flex: none; display: flex; flex-direction: column; background: var(--side); border-left: 1px solid var(--line); min-height: 0; }
  .grip { position: absolute; left: -3px; top: 0; bottom: 0; width: 6px; cursor: col-resize; z-index: 5; }
  .tabs { height: 46px; display: flex; align-items: center; gap: 1px; padding: 0 10px; overflow-x: auto; flex: none; scrollbar-width: none; }
  .tabs::-webkit-scrollbar { display: none; }
  .tab { display: inline-flex; align-items: center; height: 26px; border-radius: 8px; color: var(--ink-2); flex: none; }
  .tab:hover { background: var(--hover); }
  .tl { display: inline-flex; align-items: center; gap: 6px; height: 26px; padding: 0 2px 0 8px; border: 0; background: transparent; color: inherit; font-weight: 500; font-size: 12px; white-space: nowrap; }
  .x { width: 18px; height: 18px; margin-right: 4px; border: 0; background: transparent; color: var(--muted); border-radius: 5px; display: grid; place-items: center; opacity: 0; }
  .tab:hover .x, .tab.on .x { opacity: 1; }
  .x:hover { background: var(--hover); color: var(--ink); }
  .add { position: relative; flex: none; margin-left: 2px; }
  .plus { width: 26px; height: 26px; border: 0; background: transparent; border-radius: 8px; color: var(--muted); display: grid; place-items: center; }
  .plus:hover, .plus.on { background: var(--hover); color: var(--ink); }
  .menu { position: fixed; z-index: 60; min-width: 210px; padding: 5px; display: grid; }
  .mi { display: flex; align-items: center; gap: 8px; border: 0; background: transparent; text-align: left; padding: 7px 9px; border-radius: 8px; font-size: 12.5px; color: var(--ink); }
  .mi:hover { background: var(--hover); }
  .mi .ck { margin-left: auto; color: var(--accent); display: grid; }
  .tab.on { background: var(--card); color: var(--ink); box-shadow: inset 0 1px 0 var(--edge), 0 0 0 1px var(--line), var(--shadow); }
  .badge { min-width: 17px; height: 17px; border-radius: 9px; background: var(--warn); color: #fff; font-size: 11px; display: grid; place-items: center; padding: 0 5px; }
  .body { flex: 1; min-height: 0; margin: 0 12px 12px; display: flex; }
  .body > .inner { flex: 1; min-height: 0; display: flex; flex-direction: column; }
  .body > .inner > :global(*) { flex: 1; min-height: 0; }
  .pane.off { display: none; }
  .rp.hidden { display: none; }
</style>
