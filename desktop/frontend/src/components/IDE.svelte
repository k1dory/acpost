<script lang="ts" module>
  import { paneOwners as owners } from "./idePane";
  import { Node as N } from "../lib/api";
  // One native pane: one queue for every IDE view, made once.
  const newPaneOwner = owners({ show: (u, r) => N.IDEShow(u, r.x, r.y, r.w, r.h), hide: () => N.IDEHide() });
</script>

<script lang="ts">
  import { headerIcon } from "../lib/ui";
  import { onDestroy } from "svelte";
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node, errText } from "../lib/api";
  import { screenScale } from "../lib/format";
  import Icon from "./Icon.svelte";
  import ViewSwitch from "./ViewSwitch.svelte";
  import Logo from "./Logo.svelte";
  import { paneRect, type Rect } from "./idePane";

  // The IDE: VS Code (the one installed on this Mac, its web server) in a native view over this
  // box, opened in the chat's folder; the chat stays beside it.
  let box = $state<HTMLDivElement>();
  let err = $state("");
  let starting = $state(false);
  let url = $state("");   // the server's address for the folder
  let pending = "";        // an address not loaded yet (the pane was covered, or not placed)
  let gen = 0;             // the folder asked for last: an older answer is dropped
  let alive = true;
  const folder = $derived(app.chatCwd() || app.draft.cwd || app.home);
  const short = (d: string) => (app.home && d.startsWith(app.home) ? "~" + d.slice(app.home.length) : d);
  // What covers the box: menus drawn in HTML (the pane would be over them). A toast does not: the
  // pane is made shorter to leave it its place, VS Code keeps its focus.
  const covered = $derived(app.palette || app.viewMenu);
  // The native pane's calls, in order, the latest winning; this view is the pane's newest owner (an
  // earlier one's late calls are dropped).
  const pane = newPaneOwner();

  // Opens dir's address (again: reloads the page when the address is the one shown). The server is
  // made sure of first (started again if it ended); an answer for a folder no longer asked for is
  // dropped.
  async function request(dir: string, again: boolean) {
    if (!dir) return;
    const my = ++gen;
    starting = true; err = "";
    try {
      const u = await Node.IDEStart(dir);
      if (!alive || my !== gen) return;
      if (again && u === url) Node.IDEReload().catch(() => {});
      else { url = u; pending = u; place(); }
    } catch (e) { if (alive && my === gen) err = errText(e); }
    finally { if (alive && my === gen) starting = false; }
  }
  const open = (dir: string) => request(dir, false);
  const reload = () => request(folder, true);
  const rectOf = (e: Element): Rect => { const r = e.getBoundingClientRect(); return { x: r.left, y: r.top, w: r.width, h: r.height }; };
  // The box in window points (the text size zoom taken into account, as the preview does).
  function place() {
    if (!alive) return;
    const hide = () => { pane.hide().catch(() => {}); };
    if (!box || covered || !url) return hide();
    const z = app.textSize, k = screenScale();
    const f = Math.abs(k - z) < 0.01 ? 1 : z;
    const toast = app.toast ? document.querySelector('[role="alert"]') : null;
    const r = paneRect(rectOf(box), f, toast ? rectOf(toast) : null);
    if (!r) return hide();
    const load = pending; // loaded once shown; kept while covered
    pane.show(load, r)
      .then(ok => { if (ok && pending === load) pending = ""; })
      .catch(e => { if (alive) err = errText(e); });
  }
  const onResize = () => place();
  let opened = "";
  $effect(() => { const d = folder; if (d && d !== opened) { opened = d; open(d); } });
  $effect(() => {
    covered; app.textSize; app.toast;
    if (!box) return;
    const ro = new ResizeObserver(() => place());
    ro.observe(box);
    addEventListener("resize", onResize);
    requestAnimationFrame(() => place());
    return () => { ro.disconnect(); removeEventListener("resize", onResize); };
  });
  // VS Code's colours follow the app's theme (light or dark, the system's when it follows it).
  $effect(() => {
    app.theme;
    const send = () => Node.IDETheme(document.documentElement.dataset.theme === "dark").catch(() => {});
    const later = () => requestAnimationFrame(send); // after the app has set its own theme
    later();
    const mq = matchMedia("(prefers-color-scheme: dark)");
    mq.addEventListener("change", later);
    return () => mq.removeEventListener("change", later);
  });
  onDestroy(() => { alive = false; gen++; pane.close().catch(() => {}); });
</script>

<section class="ide">
  <!-- The window's title bar in this view: the traffic lights, the view switcher, the folder. -->
  <div class="bar drag">
    <ViewSwitch />
    <span class="muted">·</span>
    <span class="muted mono path" title={folder}>{short(folder)}</span>
    <span class="sp"></span>
    <!-- Agents waiting for an answer (the sidebar, which shows them, is hidden here). -->
    {#if app.asking.length}<button class="btn ghost sm nodrag ask" onclick={() => (app.view = "board")}>{t("{n} waiting for an answer", { n: app.asking.length })}</button>{/if}
    <button class="btn ghost icon nodrag" onclick={reload} disabled={starting} title={starting ? t("Starting VS Code… the first time it takes a minute or two.") : t("Reload")}><Icon name="refresh" size={headerIcon} /></button>
  </div>
  {#if err}<div class="err selectable">{err}</div>{/if}
  <div class="box" bind:this={box}>
    {#if starting && !url}<div class="hint"><Logo size={72} loop /><span>{t("Starting VS Code… the first time it takes a minute or two.")}</span></div>{/if}
  </div>
</section>

<style>
  .ide { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--page); }
  .bar { height: 50px; flex: none; display: flex; align-items: center; gap: 6px; padding: 0 10px 0 80px; background: var(--side); border-bottom: 1px solid var(--line-soft); font-size: 12.5px; }
  .path { font-size: 11.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
  .sp { flex: 1; }
  .ask { color: var(--warn); font-weight: 600; }
  .box { flex: 1; min-height: 0; display: grid; place-items: center; }
  .hint { font-size: 12.5px; color: var(--muted); display: grid; gap: 14px; justify-items: center; text-align: center; }
  .err { margin: 8px; padding: 8px 10px; border-radius: 10px; background: var(--crit-bg); color: var(--crit); font-size: 12px; white-space: pre-line; }
</style>
