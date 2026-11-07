<script lang="ts">
  import { onMount } from "svelte";
  import { app, applyTheme } from "./lib/state.svelte";
  import { on } from "./lib/api";
  import { Browser } from "@wailsio/runtime";
  import Sidebar from "./components/Sidebar.svelte";
  import Conversation from "./components/Conversation.svelte";
  import RightPanel from "./components/RightPanel.svelte";
  import Usage from "./components/Usage.svelte";
  import Settings from "./components/Settings.svelte";
  import Board from "./components/Board.svelte";
  import Palette from "./components/Palette.svelte";
  import Wiki from "./components/Wiki.svelte";
  import { t, i18n } from "./lib/i18n.svelte";

  onMount(() => {
    try { app.theme = (localStorage.getItem("theme") as any) || "light"; } catch {}
    applyTheme(app.theme);
    try { app.textSize = Number(localStorage.getItem("text-size")) || 1; } catch {}
    app.setTextSize(app.textSize);
    document.documentElement.lang = i18n.lang;
    app.connect();
    const offPreview = on("preview:event", e => app.notePreview(e));
    const poll = setInterval(() => { if (!app.conn?.ok) app.connect(); else { app.loadAgents(); app.loadChats(); } }, 20000);
    const asking = setInterval(() => { if (app.conn?.ok) app.watchAsking(); }, 8000);
    return () => { clearInterval(poll); clearInterval(asking); offPreview(); };
  });

  // Links never take the app's own page away. A page of this Mac (localhost, 127.0.0.1) opens in the
  // app's preview, other web pages in the system's browser, a file of this Mac as an artifact;
  // links inside the page (#…) stay its own.
  const localHost = /^(localhost|127\.\d+\.\d+\.\d+|\[::1\]|0\.0\.0\.0|[a-z0-9-]+\.localhost)$/i;
  function links(e: MouseEvent) {
    const el = e.target as HTMLElement | null;
    const code = el?.closest?.("code.pl") as HTMLElement | null;
    if (code) { e.preventDefault(); app.openArtifact({ name: code.textContent!.split("/").pop()!, path: code.textContent!, base: app.chatCwd() }); return; }
    const a = el?.closest?.("a[href]") as HTMLAnchorElement | null;
    if (!a) return;
    const href = a.getAttribute("href") ?? "";
    if (href.startsWith("#")) return;
    e.preventDefault();
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

  function keys(e: KeyboardEvent) {
    if (!e.metaKey) return;
    // Text size: ⌘+ / ⌘− / ⌘0, as in any Mac app.
    if (e.key === "=" || e.key === "+") { e.preventDefault(); app.stepTextSize(1); }
    else if (e.key === "-") { e.preventDefault(); app.stepTextSize(-1); }
    else if (e.key === "0") { e.preventDefault(); app.setTextSize(1); }
    else if (e.key === "n") { e.preventDefault(); app.newTask(); }
    else if (e.key === "\\") { e.preventDefault(); app.panel = !app.panel; }
    else if (e.key === "j") { e.preventDefault(); app.openTab("terminal"); }
    else if (e.key === ",") { e.preventDefault(); app.view = "settings"; }
    else if (e.key === "k") { e.preventDefault(); app.palette = !app.palette; }
    else if (e.key === "b") { e.preventDefault(); app.view = "board"; }
    else if (e.key === "l") {
      // The selected text, quoted into the composer.
      const t = window.getSelection()?.toString().trim();
      if (t) { e.preventDefault(); app.quote = t.split("\n").map(l => "> " + l).join("\n") + "\n\n"; if (app.view !== "chat") app.view = "chat"; }
    }
  }
</script>

<svelte:window onkeydown={keys} onclickcapture={links} />

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
  {:else}
    <Conversation />
  {/if}
  {#if app.conn?.ok}<RightPanel hidden={app.view !== "chat" || !app.panel} />{/if}
  {#if app.palette}<Palette />{/if}
  {#if app.error}
    <button class="toast card" onclick={() => (app.error = "")}>{app.error}</button>
  {/if}
</div>

<style>
  .shell { height: 100%; display: flex; min-height: 0; }
  .down { flex: 1; display: grid; place-items: center; }
  .box { padding: 26px 28px; display: grid; gap: 10px; max-width: 460px; }
  .t { font-weight: 600; font-size: 16px; }
  .toast { white-space: pre-line; position: fixed; right: 18px; bottom: 18px; padding: 12px 16px; max-width: 420px; color: var(--crit); background: var(--crit-bg); text-align: left; z-index: 50; border-color: color-mix(in srgb, var(--crit) 25%, var(--line)); }
</style>
