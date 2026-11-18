<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  let { lines, max = 400 }: { lines: { op: string; text: string; a?: number; b?: number }[]; max?: number } = $props();
  let all = $state(false);
  const shown = $derived(all ? lines : lines.slice(0, max));
</script>

<div class="diff selectable">
  {#each shown as l}
    {#if l.op === "@"}
      <div class="hunk">{l.text}</div>
    {:else}
      <div class="l {l.op === '+' ? 'add' : l.op === '-' ? 'del' : ''}">
        <span class="n">{l.a ?? ""}</span><span class="n">{l.b ?? ""}</span><span class="op">{l.op === " " ? "" : l.op}</span><span class="c">{l.text}</span>
      </div>
    {/if}
  {/each}
  {#if lines.length > shown.length}
    <button class="btn ghost sm" onclick={() => (all = true)}>{t("Show {n} more lines", { n: lines.length - shown.length })}</button>
  {/if}
</div>

<style>
  .diff { font: 12px/1.55 var(--mono); font-variant-ligatures: none; overflow: auto; container-type: inline-size; }
  .l { display: grid; grid-template-columns: 40px 40px 16px 1fr; white-space: pre; min-width: max-content; }
  .n { color: var(--muted); text-align: right; padding-right: 8px; }
  .op { color: var(--muted); }
  .add { background: var(--add); } .add .c, .add .op { color: var(--add-ink); }
  .del { background: var(--del); } .del .c, .del .op { color: var(--del-ink); }
  .hunk { color: var(--accent); background: var(--accent-bg); padding: 2px 10px; }
  /* A narrow column (the chat beside the IDE): one column of numbers — the line's own in the file
     it is in, the old file's for a line taken out — and smaller letters; the width goes to the code. */
  @container (max-width: 460px) {
    .l { grid-template-columns: 30px 12px 1fr; font-size: 11px; }
    .l .n:first-child { display: none; }
    .l.del .n:first-child { display: block; }
    .l.del .n:nth-child(2) { display: none; }
    .n { padding-right: 6px; }
    .hunk { font-size: 11px; padding: 2px 8px; }
  }
</style>
