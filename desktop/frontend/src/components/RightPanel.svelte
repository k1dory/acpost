<script lang="ts">
  import { screenScale } from "../lib/format";
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import Icon from "./Icon.svelte";
  import Activity from "./Activity.svelte";
  import Changes from "./Changes.svelte";
  import Terminal from "./Terminal.svelte";
  import Forge from "./Forge.svelte";

  let width = $state(420);
  try { width = +(localStorage.getItem("panel") ?? 420) || 420; } catch {}
  const tabs = [["activity", "Activity", "activity"], ["changes", "Changes", "diff"], ["terminal", "Terminal", "term"], ["prs", "PRs", "pr"], ["preview", "Preview", "eye"]] as const;
  const asks = $derived((app.rev, Object.values(app.turns).reduce((a, t) => a + t.waiting().length, 0)));

  function drag(e: PointerEvent) {
    const x0 = e.clientX, w0 = width, k = screenScale(); // the mouse moves in screen pixels
    const move = (ev: PointerEvent) => (width = Math.max(340, Math.min(760, w0 - (ev.clientX - x0) / k)));
    const up = () => { removeEventListener("pointermove", move); removeEventListener("pointerup", up); try { localStorage.setItem("panel", String(width)); } catch {} };
    addEventListener("pointermove", move); addEventListener("pointerup", up);
  }
</script>

<aside class="rp" style="width:{width}px">
  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="grip" onpointerdown={drag}></div>
  <div class="tabs drag">
    {#each tabs as [k, l, ic]}
      <button class="tab nodrag" class:on={app.tab === k} onclick={() => (app.tab = k)} title={k === "prs" ? t("Pull requests and CI") : t(l)}><Icon name={ic} size={14} />{t(l)}{#if k === "activity" && asks}<span class="badge">{asks}</span>{/if}</button>
    {/each}
  </div>
  <div class="card panel body"><div class="inner">
    {#if app.tab === "activity"}<Activity />
    {:else if app.tab === "changes"}<Changes />
    {:else if app.tab === "terminal"}<Terminal />
    {:else if app.tab === "prs"}<Forge />
    {:else}<div class="empty">{t("The preview opens the agent's dev server in a native view beside the conversation, and hands its errors to the agent.")}<span class="chip">{t("next")}</span></div>{/if}
  </div></div>
</aside>

<style>
  .rp { position: relative; flex: none; display: flex; flex-direction: column; background: var(--side); border-left: 1px solid var(--line); min-height: 0; }
  .grip { position: absolute; left: -3px; top: 0; bottom: 0; width: 6px; cursor: col-resize; z-index: 5; }
  .tabs { height: 46px; display: flex; align-items: center; gap: 1px; padding: 0 10px; overflow-x: auto; flex: none; scrollbar-width: none; }
  .tabs::-webkit-scrollbar { display: none; }
  .tab { display: inline-flex; align-items: center; gap: 6px; height: 26px; padding: 0 8px; border: 0; background: transparent; border-radius: 8px; color: var(--ink-2); font-weight: 500; font-size: 12px; white-space: nowrap; flex: none; }
  .tab:hover { background: var(--hover); }
  .tab.on { background: var(--card); color: var(--ink); box-shadow: inset 0 1px 0 var(--edge), 0 0 0 1px var(--line), var(--shadow); }
  .badge { min-width: 17px; height: 17px; border-radius: 9px; background: var(--warn); color: #fff; font-size: 11px; display: grid; place-items: center; padding: 0 5px; }
  .body { flex: 1; min-height: 0; margin: 0 12px 12px; display: flex; }
  .body > .inner { flex: 1; min-height: 0; }
</style>
