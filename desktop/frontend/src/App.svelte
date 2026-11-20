<script lang="ts">
  import { onMount, tick } from "svelte";
  import { fade } from "svelte/transition";
  import { app, applyTheme } from "./lib/state.svelte";
  import { on, Node } from "./lib/api";
  import { Browser } from "@wailsio/runtime";
  import Sidebar from "./components/Sidebar.svelte";
  import Logo from "./components/Logo.svelte";
  import Conversation from "./components/Conversation.svelte";
  import RightPanel from "./components/RightPanel.svelte";
  import Analytics from "./components/Analytics.svelte";
  import Settings from "./components/Settings.svelte";
  import Board from "./components/Board.svelte";
  import Palette from "./components/Palette.svelte";
  import Permissions from "./components/Permissions.svelte";
  import Wiki from "./components/Wiki.svelte";
  import Library from "./components/Library.svelte";
  import IDE from "./components/IDE.svelte";
  import ModuleView from "./components/ModuleView.svelte";
  import { t, i18n } from "./lib/i18n.svelte";
  import { hasFiles } from "./lib/paste";
  import { screenScale } from "./lib/format";
  import { sideWidth, sideDefault, sideMin, sideRest } from "./lib/sidewidth";

  // The chat's column beside the IDE (or a module): as wide as the person drags its edge, kept
  // between launches; never so wide that nothing is left of what it stands beside.
  let side = $state(sideDefault);
  try { side = +(localStorage.getItem("ide-chat") ?? sideDefault) || sideDefault; } catch {}
  let winW = $state(window.innerWidth);
  // (The text's size scales the page: asked again when it changes, measured once it is applied.)
  let scale = $state(screenScale());
  $effect(() => { app.textSize; tick().then(() => (scale = screenScale())); });
  const sideW = $derived(sideWidth(side, winW / scale));
  let dragging = $state(false);
  function dragSide(e: PointerEvent) {
    const grip = e.currentTarget as HTMLElement, x0 = e.clientX, w0 = sideW, k = screenScale(); // the mouse moves in screen pixels
    // Captured: the IDE is a native view over the page, and the pointer crosses it.
    try { grip.setPointerCapture(e.pointerId); } catch {}
    e.preventDefault();
    dragging = true;
    const move = (ev: PointerEvent) => (side = sideWidth(w0 - (ev.clientX - x0) / k, window.innerWidth / k));
    // Ended however it ends (let go, cancelled, the capture lost with the grip itself): once.
    const ends = ["pointerup", "pointercancel", "lostpointercapture"];
    const up = () => {
      if (!dragging) return;
      dragging = false;
      grip.removeEventListener("pointermove", move);
      for (const n of ends) grip.removeEventListener(n, up);
      keepSide();
    };
    grip.addEventListener("pointermove", move);
    for (const n of ends) grip.addEventListener(n, up);
  }
  function keepSide() { try { localStorage.setItem("ide-chat", String(Math.round(side))); } catch {} }
  function resetSide() { side = sideDefault; try { localStorage.removeItem("ide-chat"); } catch {} }
  // By the keyboard: the arrows move the edge (left: a wider chat), Home is the usual width.
  function keySide(e: KeyboardEvent) {
    const by = e.key === "ArrowLeft" ? 16 : e.key === "ArrowRight" ? -16 : 0;
    if (e.key === "Home") resetSide();
    else if (by) { side = sideWidth(sideW + by, window.innerWidth / screenScale()); keepSide(); }
    else return;
    e.preventDefault();
  }
  // The grip gone while it was dragged (another view opened by a key): the drag is over.
  $effect(() => { if (!app.beside) dragging = false; });

  let splashMin = $state(false), splashMax = $state(false);
  onMount(() => {
    setTimeout(() => (splashMin = true), 1300);
    setTimeout(() => (splashMax = true), 8000); // the node not answering at all: the app, not the mark forever
    try { app.theme = (localStorage.getItem("theme") as any) || "light"; } catch {}
    applyTheme(app.theme);
    try { app.textSize = Number(localStorage.getItem("text-size")) || 1; } catch {}
    app.setTextSize(app.textSize);
    document.documentElement.lang = i18n.lang;
    app.connect();
    const offPreview = on("preview:event", e => app.notePreview(e));
    const offModule = on("module:event", e => app.noteModule(e));
    const offNav = on("app:navigate", (u: string) => route(u));
    Node.GuardNavigation().catch(() => {});
    const poll = setInterval(() => { if (!app.conn?.ok) app.connect(); else { app.loadAgents(); app.loadChats(); } }, 20000);
    const asking = setInterval(() => { if (app.conn?.ok) app.watchAsking(); }, 8000);
    return () => { clearInterval(poll); clearInterval(asking); offPreview(); offModule(); offNav(); };
  });

  // Links never take the app's own page away. A page of this Mac (localhost, 127.0.0.1) opens in the
  // app's preview, other web pages in the system's browser, a file of this Mac as an artifact;
  // links inside the page (#…) stay its own.
  const localHost = /^(localhost|127\.\d+\.\d+\.\d+|\[::1\]|0\.0\.0\.0|[a-z0-9-]+\.localhost)$/i;
  function links(e: MouseEvent) {
    const el = e.target as HTMLElement | null;
    // A link in an artifact leads from the artifact's directory (the library shows any chat's;
    // "" when it has none), elsewhere from the open chat's.
    const shown = el?.closest?.("[data-artifact-base]") as HTMLElement | null;
    const base = shown ? shown.dataset.artifactBase ?? "" : app.chatCwd();
    // A relative path from an artifact with no directory leads nowhere known: not opened against
    // another chat's.
    const nowhere = (p: string) => {
      if (!shown || base || /^(\/|~|[a-z][a-z0-9+.-]*:)/i.test(p)) return false;
      app.error = t("This file has no directory of its own: a relative link from it cannot be opened.");
      return true;
    };
    const code = el?.closest?.("code.pl") as HTMLElement | null;
    if (code) { e.preventDefault(); if (!nowhere(code.textContent!)) app.openArtifact({ name: code.textContent!.split("/").pop()!, path: code.textContent!, base }); return; }
    const a = el?.closest?.("a") as Element | null; // an SVG link too (xlink:href)
    if (!a) return;
    const href = a.getAttribute("href") ?? a.getAttribute("xlink:href") ?? "";
    if (!href) return;
    if (href.startsWith("#artifact:")) {
      e.preventDefault();
      const path = decodeURIComponent(href.slice(10));
      if (!nowhere(path)) app.openArtifact({ name: path.split("/").pop() || path, path, base });
      return;
    }
    if (href.startsWith("#")) return;
    e.preventDefault();
    if (!nowhere(href)) route(href, base);
  }
  // Where an address opens: a page of this Mac in the preview, the web in the browser, a file as
  // an artifact. Also what the app's page was kept from going to (app:navigate).
  function route(href: string, base = app.chatCwd()) {
    let u: URL;
    try { u = new URL(href, "file:///"); } catch { return; }
    if (u.protocol === "http:" || u.protocol === "https:") {
      if (localHost.test(u.hostname)) app.openInPreview(u.href);
      else Browser.OpenURL(u.href);
    } else if (u.protocol === "file:") {
      const path = decodeURIComponent(href.startsWith("file:") ? u.pathname : href);
      app.openArtifact({ name: path.split("/").pop() || path, path, base });
    } else if (u.protocol === "mailto:") Browser.OpenURL(u.href);
  }

  // The error toast: closed by ×, or after 12 s; the same error closed a moment ago is not shown
  // again (a refresh every few seconds would bring it back each time).
  let shownError = $derived(app.toast);
  const closedAt = new Map<string, number>();
  let hideTimer: ReturnType<typeof setTimeout> | undefined;
  $effect(() => {
    const e = app.error;
    if (!e) return;
    app.error = "";
    if (Date.now() - (closedAt.get(e) ?? 0) < 120_000) return;
    app.toast = e;
    clearTimeout(hideTimer);
    hideTimer = setTimeout(dismiss, 12_000);
  });
  function dismiss() {
    if (app.toast) closedAt.set(app.toast, Date.now());
    app.toast = "";
    clearTimeout(hideTimer);
  }

  function keys(e: KeyboardEvent) {
    if (!e.metaKey) return;
    // Text size: ⌘+ / ⌘− / ⌘0, as in any Mac app.
    if (e.key === "=" || e.key === "+") { e.preventDefault(); app.stepTextSize(1); }
    else if (e.key === "-") { e.preventDefault(); app.stepTextSize(-1); }
    else if (e.key === "0") { e.preventDefault(); app.setTextSize(1); }
    else if (e.key === "n") { e.preventDefault(); app.newTask(); }
    else if (e.key === "\\") { e.preventDefault(); app.setPanel(!app.panel); }
    else if (e.key === "j") { e.preventDefault(); app.terminal(); }
    else if (e.key === ",") { e.preventDefault(); app.view = "settings"; }
    else if (e.key === "k") { e.preventDefault(); app.palette = !app.palette; }
    else if (e.key === "b") { e.preventDefault(); app.view = "board"; }
    else if (e.code === "KeyI" && e.shiftKey) { e.preventDefault(); app.view = app.view === "ide" ? "chat" : "ide"; }
    else if (e.key === "l") {
      // The selected text, quoted into the composer.
      const t = window.getSelection()?.toString().trim();
      if (t) { e.preventDefault(); app.quote = t.split("\n").map(l => "> " + l).join("\n") + "\n\n"; if (app.view !== "chat") app.view = "chat"; }
    }
  }
</script>

<!-- A file dropped where nothing takes it (a view without a composer) is not opened in the app's
     place: the webview would leave the app for the file. -->
<svelte:window bind:innerWidth={winW} onkeydown={keys} onclickcapture={links} onsubmitcapture={e => e.preventDefault()}
  ondragover={e => { if (hasFiles(e.dataTransfer?.types)) e.preventDefault(); }}
  ondrop={e => { if (hasFiles(e.dataTransfer?.types)) e.preventDefault(); }} />

<div class="shell">
  <Sidebar />
  {#if app.conn && !app.conn.ok}
    <div class="down">
      <div class="card box">
        <div class="t">{t("Can't reach the acpost node")}</div>
        <div class="muted selectable">{app.conn.error}</div>
        <button class="btn primary" onclick={() => app.connect()}>{t("Try again")}</button>
      </div>
    </div>
  {:else if app.view === "usage"}
    <Analytics />
  {:else if app.view === "settings"}
    <Settings />
  {:else if app.view === "board"}
    <Board />
  {:else if app.view === "wiki"}
    <Wiki />
  {:else if app.view === "artifacts"}
    <Library />
  {:else}
    <!-- The chat, and in the IDE the same chat as a column on the right (one instance: a draft being
         typed stays), VS Code taking the rest of the window. -->
    <div class="convwrap" class:ide-chat={app.beside} style:width={app.beside ? sideW + "px" : null}>
      <!-- svelte-ignore a11y_no_noninteractive_element_interactions, a11y_no_noninteractive_tabindex -->
      {#if app.beside}<div class="grip" class:on={dragging} role="separator" aria-orientation="vertical" tabindex="0" aria-label={t("The chat's width")} aria-valuenow={sideW} aria-valuemin={sideMin} aria-valuemax={Math.max(sideMin, Math.round(winW / scale) - sideRest)}
        title={t("Drag to resize; double-click for the usual width")} onpointerdown={dragSide} onkeydown={keySide} ondblclick={resetSide}></div>{/if}
      <Conversation />
    </div>
    {#if app.view === "ide"}<IDE />{:else if app.module}{#key app.module.id}<ModuleView m={app.module} />{/key}{/if}
  {/if}
  {#if app.conn?.ok}<RightPanel hidden={app.view !== "chat" || !app.panel} />{/if}
  {#if app.palette}<Palette />{/if}
  {#if app.permissionsFor !== null}<Permissions folder={app.permissionsFor} />{/if}
  <!-- Starting: the mark drawing itself while the node is reached (or started), at least once
       through, then gone. -->
  {#if (!app.conn && !splashMax) || !splashMin}<div class="splash drag" out:fade={{ duration: 260 }}><Logo size={88} loop /></div>{/if}
  {#if shownError}
    <div class="toast card" role="alert">
      <div class="tt selectable">{shownError}</div>
      <button class="x" onclick={dismiss} aria-label={t("Close")} title={t("Close")}>×</button>
    </div>
  {/if}
</div>

<style>
  .shell { height: 100%; display: flex; min-height: 0; }
  .convwrap { flex: 1; display: flex; min-width: 0; }
  .convwrap.ide-chat { flex: none; width: 400px; order: 2; border-left: 1px solid var(--line); position: relative; }
  /* Inside the column's edge: what is left of it is the IDE's native view, over the page. */
  .grip { position: absolute; left: 0; top: 0; bottom: 0; width: 9px; cursor: col-resize; z-index: 45; }
  .grip:hover, .grip.on, .grip:focus-visible { box-shadow: inset 2px 0 0 var(--accent); outline: 0; }
  .ide-chat :global(.conv) { min-width: 0; }
  .down { flex: 1; display: grid; place-items: center; }
  .splash { position: fixed; inset: 0; z-index: 80; display: grid; place-items: center; background: var(--page); }
  .box { padding: 26px 28px; display: grid; gap: 10px; max-width: 460px; }
  .t { font-weight: 600; font-size: 16px; }
  .toast { white-space: pre-line; position: fixed; left: 50%; transform: translateX(-50%); bottom: 18px; padding: 12px 12px 12px 16px; max-width: 520px; color: var(--crit); background: var(--crit-bg); text-align: left; z-index: 50; border-color: color-mix(in srgb, var(--crit) 25%, var(--line)); display: flex; gap: 10px; align-items: flex-start; font-size: 12.5px; }
  .toast .tt { flex: 1; min-width: 0; }
  .toast .x { border: 0; background: transparent; color: inherit; font-size: 18px; line-height: 1; padding: 0 4px; opacity: .7; }
  .toast .x:hover { opacity: 1; }
</style>
