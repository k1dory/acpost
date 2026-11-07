<script lang="ts">
  import { onDestroy } from "svelte";
  import { Browser } from "@wailsio/runtime";
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node, on, errText } from "../lib/api";
  import { screenScale } from "../lib/format";
  import Icon from "./Icon.svelte";

  // The preview: the page (the agent's dev server) in a native view laid over this box; what
  // goes wrong in it is listed below and can go to the agent. Covered (a menu over it), it hides.
  let { covered = false }: { covered?: boolean } = $props();

  let box = $state<HTMLDivElement>();
  let saved = "";
  try { saved = localStorage.getItem("preview-url") ?? ""; } catch {}
  let address = $state(saved || "http://localhost:3000");
  let err = $state("");
  let open = $state(false); // the list of problems shown
  const problems = $derived(app.preview.events.filter(e => e.kind !== "nav"));

  function go(u = address) {
    let url = u.trim();
    if (url && !/^https?:\/\//.test(url)) url = "http://" + url;
    address = url;
    try { localStorage.setItem("preview-url", url); } catch {}
    err = "";
    app.preview.events = [];
    Node.PreviewLoad(url).catch(e => (err = errText(e)));
  }

  // Where the box is, in the window's points: the page's coordinates times the text size zoom
  // (when the engine reports them unzoomed).
  function place() {
    if (!box || covered) { Node.PreviewHide().catch(() => {}); return; }
    const r = box.getBoundingClientRect();
    if (r.width < 4 || r.height < 4) { Node.PreviewHide().catch(() => {}); return; }
    const z = app.textSize, k = screenScale();
    const f = Math.abs(k - z) < 0.01 ? 1 : z;
    Node.PreviewShow(r.left * f, r.top * f, r.width * f, r.height * f).catch(e => (err = errText(e)));
  }
  let first = true;
  $effect(() => {
    covered; app.textSize; open; problems.length;
    if (!box) return;
    const ro = new ResizeObserver(() => place());
    ro.observe(box);
    addEventListener("resize", place);
    requestAnimationFrame(place);
    if (first) { first = false; if (!app.preview.loaded) { app.preview.loaded = true; go(); } }
    return () => { ro.disconnect(); removeEventListener("resize", place); };
  });
  // A link to a page of this Mac, clicked in a chat: shown here.
  $effect(() => { const u = app.preview.pending; if (u) { app.preview.pending = ""; go(u); } });
  onDestroy(() => { Node.PreviewHide().catch(() => {}); });

  // Only a web page leaves for the browser (not whatever address a page made up).
  function openOutside() {
    const u = app.preview.url || address;
    if (/^https?:\/\//i.test(u)) Browser.OpenURL(u);
  }
  function sendToAgent() {
    const list = problems.slice(-30).map(e => {
      const where = e.source ? ` (${e.source}${e.line ? ":" + e.line : ""})` : "";
      const stack = e.stack && !e.message?.includes(e.stack) ? "\n  " + e.stack.split("\n").slice(0, 6).join("\n  ") : "";
      return `- [${e.kind}] ${e.message ?? ""}${where}${stack}`;
    }).join("\n").slice(0, 12000);
    app.quote = t("The preview of {url} shows these problems:", { url: app.preview.url || address }) + "\n\n" + list + "\n\n";
  }
  const kindName = (k: string) => k.replace("console.", "");
</script>

<div class="pv">
  <div class="addr">
    <button class="btn ghost sm" title={t("Back")} onclick={() => Node.PreviewBack()}><Icon name="chev" size={12} /></button>
    <button class="btn ghost sm" title={t("Reload")} onclick={() => Node.PreviewReload()}><Icon name="refresh" size={13} /></button>
    <input class="input url" bind:value={address} onkeydown={e => { if (e.key === "Enter") go(); }} spellcheck="false" />
    <button class="btn ghost sm" title={t("Open in the browser")} onclick={openOutside}><Icon name="link" size={13} /></button>
  </div>
  {#if err}<div class="err selectable">{err}</div>{/if}
  <div class="box" bind:this={box}><div class="muted hint">{t("The page shows here (only in the app, not in a browser).")}</div></div>
  <div class="probs" class:open>
    <button class="ph" onclick={() => (open = !open)}>
      <span class="dot" class:failed={problems.length > 0} class:done={!problems.length}></span>
      {problems.length ? t("{n} problems in the page", { n: problems.length }) : t("No problems in the page")}
      <span class="sp"></span>
      {#if problems.length}
        <span class="link" role="button" tabindex="0" onclick={e => { e.stopPropagation(); sendToAgent(); }} onkeydown={() => {}}>{t("To the agent")}</span>
        <span class="link muted" role="button" tabindex="0" onclick={e => { e.stopPropagation(); app.preview.events = []; }} onkeydown={() => {}}>{t("Clear")}</span>
      {/if}
    </button>
    {#if open}
      <div class="list">
        {#each problems.slice(-100) as e, i (i)}
          <div class="pr">
            <span class="k" class:warn={e.kind === "console.warn"}>{kindName(e.kind)}</span>
            <div class="m selectable"><div>{e.message}</div>{#if e.source}<div class="muted mono src">{e.source}{e.line ? ":" + e.line : ""}</div>{/if}</div>
          </div>
        {/each}
      </div>
    {/if}
  </div>
</div>

<style>
  .pv { height: 100%; display: flex; flex-direction: column; min-height: 0; }
  .addr { display: flex; align-items: center; gap: 4px; padding: 6px; border-bottom: 1px solid var(--line-soft); }
  .addr .btn:first-child :global(svg) { transform: rotate(180deg); }
  .url { flex: 1; height: 28px; font: 12px var(--mono); }
  .box { flex: 1; min-height: 80px; position: relative; display: grid; place-items: center; background: var(--page); }
  .hint { font-size: 12px; padding: 20px; text-align: center; }
  .probs { border-top: 1px solid var(--line-soft); flex: none; max-height: 45%; display: flex; flex-direction: column; }
  .ph { display: flex; align-items: center; gap: 8px; border: 0; background: transparent; padding: 8px 10px; font-size: 12px; color: var(--ink-2); text-align: left; }
  .sp { flex: 1; }
  .link { color: var(--accent); font-weight: 500; cursor: default; }
  .link.muted { color: var(--muted); }
  .list { overflow: auto; padding: 0 8px 8px; display: grid; gap: 4px; }
  .pr { display: flex; gap: 8px; font-size: 12px; padding: 6px 4px; border-top: 1px solid var(--line-soft); }
  .k { flex: none; font: 600 10.5px var(--mono); color: var(--crit); min-width: 64px; }
  .k.warn { color: var(--warn); }
  .m { min-width: 0; overflow-wrap: anywhere; }
  .src { font-size: 10.5px; }
  .err { margin: 6px; padding: 8px 10px; border-radius: 10px; background: var(--crit-bg); color: var(--crit); font-size: 12px; white-space: pre-line; }
</style>
