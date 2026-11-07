<script lang="ts">
  import { onDestroy } from "svelte";
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node, errText } from "../lib/api";
  import { screenScale } from "../lib/format";
  import Icon from "./Icon.svelte";

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
  const covered = $derived(app.palette || !!app.toast);

  async function open(dir: string) {
    if (!dir) return;
    const my = ++gen;
    starting = true; err = "";
    try {
      const u = await Node.IDEStart(dir);
      if (!alive || my !== gen) return;
      url = u; pending = u; place();
    } catch (e) { if (alive && my === gen) err = errText(e); }
    if (alive && my === gen) starting = false;
  }
  // The box in window points (the text size zoom taken into account, as the preview does).
  function place() {
    if (!alive) return;
    if (!box || covered || !url) { Node.IDEHide().catch(() => {}); return; }
    const r = box.getBoundingClientRect();
    if (r.width < 4 || r.height < 4) { Node.IDEHide().catch(() => {}); return; }
    const z = app.textSize, k = screenScale();
    const f = Math.abs(k - z) < 0.01 ? 1 : z;
    const load = pending; // loaded once shown; kept while covered
    Node.IDEShow(load, r.left * f, r.top * f, r.width * f, r.height * f)
      .then(() => { if (pending === load) pending = ""; })
      .catch(e => (err = errText(e)));
  }
  const onResize = () => place();
  let opened = "";
  $effect(() => { const d = folder; if (d && d !== opened) { opened = d; open(d); } });
  $effect(() => {
    covered; app.textSize;
    if (!box) return;
    const ro = new ResizeObserver(() => place());
    ro.observe(box);
    addEventListener("resize", onResize);
    requestAnimationFrame(() => place());
    return () => { ro.disconnect(); removeEventListener("resize", onResize); };
  });
  onDestroy(() => { alive = false; gen++; Node.IDEHide().catch(() => {}); });
</script>

<section class="ide">
  <div class="bar">
    <Icon name="term" size={13} />
    <b>VS Code</b>
    <span class="muted mono path" title={folder}>{short(folder)}</span>
    <span class="sp"></span>
    <button class="btn ghost sm" onclick={() => (url ? Node.IDEReload() : open(folder))} title={t("Reload")}><Icon name="refresh" size={13} /></button>
    <button class="btn ghost sm" onclick={() => Node.IDEOpenApp(folder).catch(e => (err = errText(e)))}>{t("Open in VS Code")}</button>
  </div>
  {#if err}<div class="err selectable">{err}</div>{/if}
  <div class="box" bind:this={box}>
    {#if starting && !url}<div class="hint"><span class="spin"></span> {t("Starting VS Code… the first time it takes a minute or two.")}</div>{/if}
  </div>
</section>

<style>
  .ide { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--page); border-left: 1px solid var(--line-soft); }
  .bar { height: 40px; flex: none; display: flex; align-items: center; gap: 8px; padding: 0 10px 0 14px; border-bottom: 1px solid var(--line-soft); font-size: 12.5px; }
  .path { font-size: 11.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
  .sp { flex: 1; }
  .box { flex: 1; min-height: 0; display: grid; place-items: center; }
  .hint { font-size: 12.5px; color: var(--muted); display: flex; gap: 8px; align-items: center; }
  .err { margin: 8px; padding: 8px 10px; border-radius: 10px; background: var(--crit-bg); color: var(--crit); font-size: 12px; white-space: pre-line; }
</style>
