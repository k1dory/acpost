<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { onDestroy, untrack } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import "@xterm/xterm/css/xterm.css";
  import { app } from "../lib/state.svelte";
  import { Node, on, b64, unb64, errText } from "../lib/api";
  import { attachShell } from "./terminalAttach";

  // Shown (its tab current, the panel open): it takes the keys, sized to its box.
  let { active = true }: { active?: boolean } = $props();
  let el: HTMLDivElement;
  let fitter: FitAddon | null = null;
  let term: Terminal | null = null;
  let id = "";
  let err = $state("");
  let exited = $state<string>("");
  let lost = $state(false); // the connection to the node is down; the shell may go on
  // Each start (and close) is a new generation: a shell that opens after the component moved on
  // (closed, or started again) is closed, not attached.
  let gen = 0;
  let where = $state(""); // the agent whose directory the shell was opened in
  let stop: (() => void) | null = null;
  let ro: ResizeObserver | null = null;
  // Keystrokes go one request at a time, in order: what is typed meanwhile goes in the next.
  let queued = "";
  let sending = false;
  async function type(s: string) {
    queued += s;
    if (sending) return;
    sending = true;
    while (queued && id) {
      const chunk = queued;
      queued = "";
      try { await Node.TermInput(id, b64(chunk)); } catch { break; }
    }
    sending = false;
  }

  function css(v: string) { return getComputedStyle(document.documentElement).getPropertyValue(v).trim(); }
  // The terminal's colours follow the app's theme. On the light one xterm's own palette (made for
  // a dark ground) leaves white and yellow unreadable: each theme has its own sixteen colours.
  const light = {
    black: "#1a1a18", red: "#c23434", green: "#1f8a3b", yellow: "#9a6a00", blue: "#2f5bd3", magenta: "#8e3fae", cyan: "#0e7c86", white: "#8c8a83",
    brightBlack: "#6b6a64", brightRed: "#d9453f", brightGreen: "#2a9d4a", brightYellow: "#b07d10", brightBlue: "#4a72e0", brightMagenta: "#a259c4", brightCyan: "#1595a0", brightWhite: "#4a4944",
  };
  const dark = {
    black: "#2b2a27", red: "#ef6a6a", green: "#6fd389", yellow: "#e5c07b", blue: "#7d9bf0", magenta: "#c792ea", cyan: "#5fc7d1", white: "#c9c7bf",
    brightBlack: "#8c8a83", brightRed: "#f19a9a", brightGreen: "#8fe0a6", brightYellow: "#f0d398", brightBlue: "#a2b8f5", brightMagenta: "#d8b0f0", brightCyan: "#8ad8df", brightWhite: "#f2f1ec",
  };
  function palette() {
    const isDark = document.documentElement.dataset.theme === "dark";
    return {
      ...(isDark ? dark : light),
      background: css("--card"), foreground: css("--ink"), cursor: css("--ink"), cursorAccent: css("--card"),
      selectionBackground: isDark ? "rgba(125,155,240,.30)" : "rgba(47,91,211,.18)",
    };
  }
  // A theme changed after the shell opened recolours it in place.
  const themeWatch = new MutationObserver(() => { if (term) term.options.theme = palette(); });
  themeWatch.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });

  async function start() {
    close();
    const my = gen;
    err = ""; exited = ""; lost = false;
    const fit = new FitAddon();
    term = new Terminal({
      fontFamily: '"JetBrains Mono", "JetBrainsMono Nerd Font", "Symbols Nerd Font Mono", "MesloLGS NF", ui-monospace, Menlo, monospace', fontSize: 12.5, lineHeight: 1.25, cursorBlink: true,
      theme: palette(),
    });
    term.loadAddon(fit);
    fitter = fit;
    term.open(el);
    fit.fit();
    try {
      where = app.focusAgent();
      const opened: any = await Node.OpenTerminal(where, term.cols, term.rows);
      if (my !== gen || !term) { Node.CloseTerminal(opened.id).catch(() => {}); return; }
      id = opened.id;
      const dec = new TextDecoder();
      // Subscribed before the output starts: what the shell prints first is not lost.
      stop = on("term:" + id, d => {
        if (d.data) { lost = false; term?.write(dec.decode(unb64(d.data), { stream: true })); }
        if (d.lost) lost = true;
        if (d.exit) { lost = false; exited = d.error ? errText(d.error) : d.code !== undefined && d.code !== null ? t("exited ({code})", { code: d.code }) : t("exited"); }
      });
      const xt = term, tid = id;
      await attachShell({ onData: fn => xt.onData(fn), input: type, attach: () => Node.TermAttach(tid, 0) });
      if (my !== gen || !term) return;
      term.onResize(({ cols, rows }) => Node.TermResize(id, cols, rows).catch(() => {}));
      ro = new ResizeObserver(() => fit.fit());
      ro.observe(el);
      term.focus();
    } catch (e) { if (my === gen) err = errText(e); }
  }
  function close() {
    gen++;
    stop?.(); stop = null; ro?.disconnect(); ro = null;
    if (id) Node.CloseTerminal(id).catch(() => {});
    id = ""; queued = ""; term?.dispose(); term = null;
  }
  // Started once, untracked: a chat opened meanwhile does not restart the shell (New shell does,
  // in that chat's agent's directory).
  $effect(() => { if (el) untrack(start); return close; });
  $effect(() => {
    if (!active) return;
    requestAnimationFrame(() => { try { fitter?.fit(); } catch {} term?.focus(); });
  });
  onDestroy(() => { close(); themeWatch.disconnect(); });
</script>

<div class="tw">
  <div class="tools">
    <span class="muted">{where ? t("in {agent}'s directory", { agent: where.slice(6) }) : t("home directory")}</span>
    {#if exited}<span class="chip">{exited}</span>{:else if lost}<span class="chip">{t("Not reachable right now")}</span>{/if}
    <span class="sp"></span>
    <button class="btn sm" onclick={start}>{t("New shell")}</button>
  </div>
  {#if err}<div class="empty">{err}</div>{/if}
  <div class="xt" bind:this={el}></div>
</div>

<style>
  .tw { height: 100%; display: flex; flex-direction: column; }
  .tools { display: flex; align-items: center; gap: 8px; padding: 8px; border-bottom: 1px solid var(--line-soft); font-size: 12.5px; }
  .sp { flex: 1; }
  .xt { flex: 1; min-height: 0; padding: 8px 4px 4px 10px; }
  .xt :global(.xterm) { height: 100%; }
  .xt :global(.xterm-viewport) { scrollbar-width: thin; }
</style>
