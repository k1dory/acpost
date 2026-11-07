<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { onDestroy } from "svelte";
  import Icon from "./Icon.svelte";

  // The view switcher in the window's title bar (beside the traffic lights): the app's own
  // layout, or the IDE (VS Code with the chat beside it).
  type V = { view: "chat" | "ide"; label: string; icon: string; key: string };
  const views: V[] = [
    { view: "chat", label: "Agents view", icon: "chat", key: "" },
    { view: "ide", label: "IDE view", icon: "term", key: "⇧⌘I" },
  ];
  let root: HTMLDivElement;
  const cur = $derived(app.view === "ide" ? views[1] : views[0]);
  // Open here (two of these are mounted: the sidebar's, hidden in the IDE, and the IDE's own); the
  // app is told, so the native IDE view hides while the menu is over it.
  let open = $state(false);
  function set(o: boolean) { open = o; app.viewMenu = o; }
  // The view changed by a key while open: closed (a hidden one must not keep the IDE covered).
  let at = app.view;
  $effect(() => { if (app.view !== at) { at = app.view; if (open) set(false); } });
  onDestroy(() => { if (open) app.viewMenu = false; });
  function go(v: V["view"]) { set(false); app.view = v; }
</script>

<svelte:window onclick={e => { if (open && !root.contains(e.target as Node)) set(false); }}
  onkeydown={e => { if (open && e.key === "Escape") set(false); }} />

<div class="vs nodrag" bind:this={root}>
  <button class="trig" class:on={open} onclick={() => set(!open)}>
    <span>{t(cur.label)}</span>
    <span class="chev"><Icon name="chev" size={9} /></span>
  </button>
  {#if open}
    <div class="menu card">
      {#each views as v (v.view)}
        <button class="mi" class:sel={v.view === cur.view} onclick={() => go(v.view)}>
          <Icon name={v.icon} size={13} />
          <span class="l">{t(v.label)}</span>
          {#if v.key}<kbd>{v.key}</kbd>{/if}
        </button>
      {/each}
    </div>
  {/if}
</div>

<style>
  .vs { position: relative; display: inline-block; }
  .trig { display: inline-flex; align-items: center; gap: 5px; height: 24px; padding: 0 8px; border: 0; border-radius: 7px; background: transparent; color: var(--ink); font-size: 12.5px; font-weight: 600; cursor: default; }
  .trig:hover, .trig.on { background: var(--hover); }
  .chev { display: grid; color: var(--muted); transform: rotate(90deg); }
  .menu { position: absolute; top: 28px; left: 0; z-index: 70; min-width: 200px; padding: 5px; display: grid; }
  .mi { display: flex; align-items: center; gap: 9px; border: 0; background: transparent; text-align: left; padding: 7px 9px; border-radius: 7px; font-size: 12.5px; color: var(--ink-2); }
  .mi:hover { background: var(--hover); color: var(--ink); }
  .mi.sel { color: var(--ink); font-weight: 600; }
  .l { flex: 1; }
  kbd { font: 10.5px var(--mono); color: var(--muted); }
</style>
