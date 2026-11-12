<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { onDestroy } from "svelte";
  import Icon from "./Icon.svelte";
  import { Node } from "../lib/api";
  import { screenScale } from "../lib/format";

  // The view switcher in the window's title bar (beside the traffic lights): the app's own
  // layout, or the IDE (VS Code with the chat beside it).
  // Then the modules the person added (a page beside the chat), and where one is added.
  type V = { view: typeof app.view; label: string; icon: string; key: string; own?: boolean };
  const views = $derived<V[]>([
    { view: "chat", label: "Agents view", icon: "chat", key: "" },
    { view: "ide", label: "IDE view", icon: "term", key: "⇧⌘I" },
    ...app.modules.map(m => ({ view: `mod:${m.id}` as const, label: m.name, icon: "panel", key: "", own: true })),
    { view: "settings", label: "Add a module…", icon: "plus", key: "" },
  ]);
  // A module's name is the person's: shown as it is.
  const name = (v: V) => (v.own ? v.label : t(v.label));
  let root: HTMLDivElement;
  const cur = $derived(views.find(v => v.view === app.view && v.view !== "settings") ?? views[0]);
  // Open here (two of these are mounted: the sidebar's, hidden in the IDE, and the IDE's own); the
  // app is told, so the native IDE view hides while the menu is over it.
  let open = $state(false);
  function set(o: boolean) { open = o; app.viewMenu = o; }
  // The view changed by a key while open: closed (a hidden one must not keep the IDE covered).
  let at = app.view;
  $effect(() => { if (app.view !== at) { at = app.view; if (open) set(false); } });
  onDestroy(() => { if (open) app.viewMenu = false; });
  // The system's menu (over VS Code's native pane, which nothing of the page can be); the page's
  // own only where there is no window of the app's (the app served to a browser).
  let btn: HTMLButtonElement;
  async function pop() {
    if (open) { set(false); return; }
    const r = btn.getBoundingClientRect();
    const z = app.textSize, k = screenScale();
    const f = Math.abs(k - z) < 0.01 ? 1 : z;
    let i = -2;
    // The entries as the menu shows them: what is chosen is one of these, whatever the list is by then.
    const list = views;
    try { i = await Node.Menu(r.left * f, (r.bottom + 4) * f, list.map(name), list.map(v => (v.key ? "I" : "")), list.indexOf(cur)); } catch {}
    if (i === -2) set(true);
    else if (i >= 0 && list[i]) go(list[i].view);
  }
  function go(v: V["view"]) {
    set(false);
    if (v === "settings") app.settingsAt = "modules";
    app.view = v;
  }
</script>

<svelte:window onclick={e => { if (open && !root.contains(e.target as Node)) set(false); }}
  onkeydown={e => { if (open && e.key === "Escape") set(false); }} />

<div class="vs nodrag" bind:this={root}>
  <button class="trig" class:on={open} bind:this={btn} onclick={pop}>
    <span>{name(cur)}</span>
    <span class="chev"><Icon name="chev" size={9} /></span>
  </button>
  {#if open}
    <div class="menu card">
      {#each views as v (v.view)}
        <button class="mi" class:sel={v === cur} onclick={() => go(v.view)}>
          <Icon name={v.icon} size={13} />
          <span class="l">{name(v)}</span>
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
