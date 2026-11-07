<script lang="ts">
  import { onMount } from "svelte";
  import { app, applyTheme } from "./lib/state.svelte";
  import Sidebar from "./components/Sidebar.svelte";
  import Conversation from "./components/Conversation.svelte";
  import RightPanel from "./components/RightPanel.svelte";
  import Usage from "./components/Usage.svelte";
  import Settings from "./components/Settings.svelte";
  import Board from "./components/Board.svelte";
  import Palette from "./components/Palette.svelte";
  import { t, i18n } from "./lib/i18n.svelte";

  onMount(() => {
    try { app.theme = (localStorage.getItem("theme") as any) || "light"; } catch {}
    applyTheme(app.theme);
    try { app.textSize = Number(localStorage.getItem("text-size")) || 1; } catch {}
    app.setTextSize(app.textSize);
    document.documentElement.lang = i18n.lang;
    app.connect();
    const poll = setInterval(() => { if (!app.conn?.ok) app.connect(); else { app.loadAgents(); app.loadChats(); } }, 20000);
    const asking = setInterval(() => { if (app.conn?.ok) app.watchAsking(); }, 8000);
    return () => { clearInterval(poll); clearInterval(asking); };
  });

  function keys(e: KeyboardEvent) {
    if (!e.metaKey) return;
    // Text size: ⌘+ / ⌘− / ⌘0, as in any Mac app.
    if (e.key === "=" || e.key === "+") { e.preventDefault(); app.stepTextSize(1); }
    else if (e.key === "-") { e.preventDefault(); app.stepTextSize(-1); }
    else if (e.key === "0") { e.preventDefault(); app.setTextSize(1); }
    else if (e.key === "n") { e.preventDefault(); app.newTask(); }
    else if (e.key === "\\") { e.preventDefault(); app.panel = !app.panel; }
    else if (e.key === "j") { e.preventDefault(); app.panel = true; app.tab = "terminal"; }
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

<svelte:window onkeydown={keys} />

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
  {:else}
    <Conversation />
    {#if app.panel}<RightPanel />{/if}
  {/if}
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
