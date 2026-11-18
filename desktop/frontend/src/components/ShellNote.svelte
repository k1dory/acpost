<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import type { ShellNote } from "../lib/shell";
  import Icon from "./Icon.svelte";

  // A command the person ran ("!git status") that the agent was told of: the command and how it
  // ended, on one line; what it printed is the agent's to read, and unfolds for the person.
  // quoted: read back from a record the app did not write (a session's history from elsewhere) —
  // it is shown as that text says, "$ command", not with the mark of a command run from here.
  let { note, quoted = false }: { note: ShellNote; quoted?: boolean } = $props();
  let open = $state(false);
  const lines = $derived(note.output ? note.output.split("\n").length : 0);
  const code = $derived(/^exit (-?\d+)$/.exec(note.end)?.[1] ?? "");
</script>

<div class="sn card">
  <button class="h" class:more={!!note.output} onclick={() => note.output && (open = !open)} aria-expanded={open}>
    <span class="mono cmd" title={note.cmd}>{#if quoted}<span class="muted">$ </span>{:else}<span class="bang">!</span>{/if}{note.cmd}</span>
    <span class="sp"></span>
    {#if code}<span class="chip crit" title={t("Exit code")}>{code}</span>
    {:else if note.end === "stopped"}<span class="chip">{t("stopped")}</span>
    {:else if note.end.startsWith("ran too long")}<span class="chip warn">{t("ran too long")}</span>
    {:else if note.end.startsWith("not run")}<span class="chip crit">{t("failed")}</span>{/if}
    {#if note.output}<span class="muted num small">{t("{n} lines", { n: lines })}</span><span class="chev" class:rot={open}><Icon name="chev" size={12} /></span>
    {:else}<span class="muted small">{t("No output")}</span>{/if}
  </button>
  <!-- Unfolded, the command is there whole too (its line above cuts a long one), to select and copy. -->
  {#if open}<pre class="out selectable"><span class="muted">$</span> {note.cmd}{"\n"}{note.output}</pre>{/if}
</div>

<style>
  /* As a tool's row is (ToolRow.svelte): the same card, height and output. */
  .sn { border-radius: var(--r-row); border-color: var(--line-soft); box-shadow: none; overflow: hidden; }
  .h { width: 100%; display: flex; align-items: center; gap: 8px; min-height: 34px; padding: 6px 12px; border: 0; background: transparent; text-align: left; }
  .h:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .h.more:hover { background: var(--hover); }
  .h > :not(.cmd):not(.sp) { flex: none; white-space: nowrap; }
  .cmd { font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; flex: 0 1 auto; }
  .bang { color: var(--accent); font-weight: 700; margin-right: 2px; }
  .sp { flex: 1 0 0; }
  .small { font-size: 11px; }
  .chev { color: var(--muted); transition: transform .15s; display: grid; } .chev.rot { transform: rotate(90deg); }
  /* As a terminal shows it: lines are not folded (a table stays a table), the box scrolls. */
  .out { margin: 0; padding: 10px 12px; max-height: 320px; overflow: auto; border-top: 1px solid var(--line-soft); background: var(--page); font: 12px/1.45 var(--mono); white-space: pre; }
</style>
