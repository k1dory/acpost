<script lang="ts" module>
  import { paneOwners as owners } from "./idePane";
  import { Node as N } from "../lib/api";
  // The modules' native panes: one queue for all of them (a module leaving the screen and the next
  // one coming are told in order). What the queue calls an address is here the module's id: the
  // native side opens the address kept for it.
  const dark = () => document.documentElement.dataset.theme === "dark";
  const newPaneOwner = owners({ show: (id, r) => N.ModuleShow(id, r.x, r.y, r.w, r.h, dark()), hide: () => N.ModuleHide(), reload: id => N.ModuleReload(id) });
</script>

<script lang="ts">
  import { headerIcon } from "../lib/ui";
  import { onDestroy } from "svelte";
  import { Browser } from "@wailsio/runtime";
  import { t } from "../lib/i18n.svelte";
  import { app, type Module } from "../lib/state.svelte";
  import { errText } from "../lib/api";
  import { screenScale } from "../lib/format";
  import Icon from "./Icon.svelte";
  import ViewSwitch from "./ViewSwitch.svelte";
  import { paneRect, type Rect } from "./idePane";

  // A module: a page the person added, in a native view over this box; the chat stays beside it.
  let { m }: { m: Module } = $props();
  let box = $state<HTMLDivElement>();
  let err = $state("");
  let alive = true;
  // What covers the box: menus drawn in HTML (the pane would be over them).
  const covered = $derived(app.palette || app.viewMenu);
  const pane = newPaneOwner();
  const rectOf = (e: Element): Rect => { const r = e.getBoundingClientRect(); return { x: r.left, y: r.top, w: r.width, h: r.height }; };
  function place() {
    if (!alive) return;
    const hide = () => { pane.hide().catch(() => {}); };
    if (!box || covered) return hide();
    const z = app.textSize, k = screenScale();
    const f = Math.abs(k - z) < 0.01 ? 1 : z;
    const toast = app.toast ? document.querySelector('[role="alert"]') : null;
    const r = paneRect(rectOf(box), f, toast ? rectOf(toast) : null);
    if (!r) return hide();
    pane.show(m.id, r).then(() => { err = ""; }).catch(e => { if (alive) err = errText(e); });
  }
  const onResize = () => place();
  $effect(() => {
    covered; app.textSize; app.toast; app.theme; m.url;
    if (!box) return;
    const ro = new ResizeObserver(() => place());
    ro.observe(box);
    addEventListener("resize", onResize);
    requestAnimationFrame(() => place());
    return () => { ro.disconnect(); removeEventListener("resize", onResize); };
  });
  onDestroy(() => { alive = false; pane.close().catch(() => {}); });
</script>

<section class="mod">
  <!-- The window's title bar in this view: the traffic lights, the view switcher, the address. -->
  <div class="bar drag">
    <ViewSwitch />
    <span class="muted">·</span>
    <span class="muted mono path" title={m.url}>{m.url}</span>
    <span class="sp"></span>
    {#if app.asking.length}<button class="btn ghost sm nodrag ask" onclick={() => (app.view = "board")}>{t("{n} waiting for an answer", { n: app.asking.length })}</button>{/if}
    <button class="btn ghost icon nodrag" onclick={() => Browser.OpenURL(m.url)} title={t("Open in the browser")} aria-label={t("Open in the browser")}><Icon name="link" size={headerIcon} /></button>
    <button class="btn ghost icon nodrag" onclick={() => pane.reload(m.id).catch(() => {})} title={t("Back to the module's address")} aria-label={t("Back to the module's address")}><Icon name="refresh" size={headerIcon} /></button>
  </div>
  {#if err}<div class="err selectable">{err}</div>{/if}
  <!-- The page did not load (the service is down, the name does not resolve, its certificate is
       refused): said here, over nothing (the pane is blank), with a way to try again. -->
  {#if app.moduleErr[m.id]}
    <div class="err selectable"><span>{app.moduleErr[m.id]}</span>
      <button class="btn sm nodrag" onclick={() => pane.reload(m.id).catch(() => {})}>{t("Try again")}</button></div>
  {/if}
  <div class="box" bind:this={box}></div>
</section>

<style>
  .mod { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--page); }
  .bar { height: 50px; flex: none; display: flex; align-items: center; gap: 6px; padding: 0 10px 0 80px; background: var(--side); border-bottom: 1px solid var(--line-soft); font-size: 12.5px; }
  .path { font-size: 11.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
  .sp { flex: 1; }
  .ask { color: var(--warn); font-weight: 600; }
  .box { flex: 1; min-height: 0; }
  .err { display: flex; align-items: center; gap: 10px; justify-content: space-between; margin: 8px; padding: 8px 10px; border-radius: 10px; background: var(--crit-bg); color: var(--crit); font-size: 12px; white-space: pre-line; }
</style>
