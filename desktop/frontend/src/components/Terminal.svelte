<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { onDestroy, untrack } from "svelte";
  import { Terminal } from "@xterm/xterm";
  import { FitAddon } from "@xterm/addon-fit";
  import "@xterm/xterm/css/xterm.css";
  import { app } from "../lib/state.svelte";
  import { Node, on, b64, unb64, errText } from "../lib/api";

  let el: HTMLDivElement;
  let term: Terminal | null = null;
  let id = "";
  let err = $state("");
  let exited = $state<string>("");
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

  async function start() {
    close();
    err = ""; exited = "";
    const fit = new FitAddon();
    term = new Terminal({
      fontFamily: '"JetBrains Mono", "JetBrainsMono Nerd Font", "Symbols Nerd Font Mono", "MesloLGS NF", ui-monospace, Menlo, monospace', fontSize: 12.5, lineHeight: 1.25, cursorBlink: true,
      theme: { background: css("--card"), foreground: css("--ink"), cursor: css("--ink"), selectionBackground: "rgba(47,91,211,.25)" },
    });
    term.loadAddon(fit);
    term.open(el);
    fit.fit();
    try {
      where = app.focusAgent();
      const opened: any = await Node.OpenTerminal(where, term.cols, term.rows);
      id = opened.id;
      const dec = new TextDecoder();
      stop = on("term:" + id, d => {
        if (d.data) term?.write(dec.decode(unb64(d.data), { stream: true }));
        if (d.exit) exited = d.error ? d.error : `exited${d.code !== undefined && d.code !== null ? ` (${d.code})` : ""}`;
      });
      term.onData(type);
      term.onResize(({ cols, rows }) => Node.TermResize(id, cols, rows).catch(() => {}));
      ro = new ResizeObserver(() => fit.fit());
      ro.observe(el);
      term.focus();
    } catch (e) { err = errText(e); }
  }
  function close() {
    stop?.(); stop = null; ro?.disconnect(); ro = null;
    if (id) Node.CloseTerminal(id).catch(() => {});
    id = ""; queued = ""; term?.dispose(); term = null;
  }
  // Started once, untracked: a chat opened meanwhile does not restart the shell (New shell does,
  // in that chat's agent's directory).
  $effect(() => { if (el) untrack(start); return close; });
  onDestroy(close);
</script>

<div class="tw">
  <div class="tools">
    <span class="muted">{where ? t("in {agent}'s directory", { agent: where.slice(6) }) : t("home directory")}</span>
    {#if exited}<span class="chip">{exited}</span>{/if}
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
</style>
