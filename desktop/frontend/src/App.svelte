<script lang="ts">
  import { onMount } from "svelte";
  import { fade } from "svelte/transition";
  import { app, applyTheme } from "./lib/state.svelte";
  import { on, Node } from "./lib/api";
  import { Browser } from "@wailsio/runtime";
  import Sidebar from "./components/Sidebar.svelte";
  import Logo from "./components/Logo.svelte";
  import Conversation from "./components/Conversation.svelte";
  import RightPanel from "./components/RightPanel.svelte";
  import Usage from "./components/Usage.svelte";
  import Settings from "./components/Settings.svelte";
  import Board from "./components/Board.svelte";
  import Palette from "./components/Palette.svelte";
  import Wiki from "./components/Wiki.svelte";
  import Library from "./components/Library.svelte";
  import IDE from "./components/IDE.svelte";
  import { t, i18n } from "./lib/i18n.svelte";

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
    const offNav = on("app:navigate", (u: string) => route(u));
    Node.GuardNavigation().catch(() => {});
    const poll = setInterval(() => { if (!app.conn?.ok) app.connect(); else { app.loadAgents(); app.loadChats(); } }, 20000);
    const asking = setInterval(() => { if (app.conn?.ok) app.watchAsking(); }, 8000);
    return () => { clearInterval(poll); clearInterval(asking); offPreview(); offNav(); };
  });

  // Links never take the app's own page away. A page of this Mac (localhost, 127.0.0.1) opens in the
  // app's preview, other web pages in the system's browser, a file of this Mac as an artifact;
  // links inside the page (#…) stay its own.
  const localHost = /^(localhost|127\.\d+\.\d+\.\d+|\[::1\]|0\.0\.0\.0|[a-z0-9-]+\.localhost)$/i;
  function links(e: MouseEvent) {
    const el = e.target as HTMLElement | null;
    const code = el?.closest?.("code.pl") as HTMLElement | null;
    if (code) { e.preventDefault(); app.openArtifact({ name: code.textContent!.split("/").pop()!, path: code.textContent!, base: app.chatCwd() }); return; }
    const a = el?.closest?.("a") as Element | null; // an SVG link too (xlink:href)
    if (!a) return;
    const href = a.getAttribute("href") ?? a.getAttribute("xlink:href") ?? "";
    if (!href) return;
    if (href.startsWith("#artifact:")) {
      e.preventDefault();
      const path = decodeURIComponent(href.slice(10));
      app.openArtifact({ name: path.split("/").pop() || path, path, base: app.chatCwd() });
      return;
    }
    if (href.startsWith("#")) return;
    e.preventDefault();
    route(href);
  }
  // Where an address opens: a page of this Mac in the preview, the web in the browser, a file as
  // an artifact. Also what the app's page was kept from going to (app:navigate).
  function route(href: string) {
    let u: URL;
    try { u = new URL(href, "file:///"); } catch { return; }
    if (u.protocol === "http:" || u.protocol === "https:") {
      if (localHost.test(u.hostname)) app.openInPreview(u.href);
      else Browser.OpenURL(u.href);
    } else if (u.protocol === "file:") {
      const path = decodeURIComponent(href.startsWith("file:") ? u.pathname : href);
      app.openArtifact({ name: path.split("/").pop() || path, path, base: app.chatCwd() });
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
    else if (e.key === "j") { e.preventDefault(); app.openTab("terminal"); }
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

<svelte:window onkeydown={keys} onclickcapture={links} onsubmitcapture={e => e.preventDefault()} />

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
    <Usage />
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
    <div class="convwrap" class:ide-chat={app.view === "ide"}><Conversation /></div>
    {#if app.view === "ide"}<IDE />{/if}
  {/if}
  {#if app.conn?.ok}<RightPanel hidden={app.view !== "chat" || !app.panel} />{/if}
  {#if app.palette}<Palette />{/if}
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
  .convwrap.ide-chat { flex: none; width: 400px; order: 2; border-left: 1px solid var(--line); }
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
