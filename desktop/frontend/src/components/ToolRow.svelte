<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { intent, toolName, toolState, editTotals, lineDiff, type Tool } from "../lib/turn";
  import { dur } from "../lib/format";
  import Icon from "./Icon.svelte";
  import DiffLines from "./DiffLines.svelte";

  let { tool, final }: { tool: Tool; final: boolean } = $props();
  let open = $state(false);
  const st = $derived(toolState(tool, final));
  const e = $derived(editTotals(tool));
  // A replayed history has no real timing: no duration then.
  const took = $derived(tool.ended && tool.ended - tool.started >= 5 ? dur(tool.ended - tool.started) : "");
  const cmd = $derived(typeof tool.input?.command === "string" ? tool.input.command : Array.isArray(tool.input?.command) ? tool.input.command.join(" ") : "");
  const hasMore = $derived(!!(tool.output || tool.edits?.length || tool.input));
</script>

<div class="tool" class:failed={st === "failed"}>
  <button class="head" onclick={() => hasMore && (open = !open)} class:more={hasMore}>
    <span class="st">
      {#if st === "running"}<span class="spin"></span>
      {:else if st === "failed"}<span class="bad"><Icon name="x" size={12} /></span>
      {:else if st === "pending"}<span class="dot queued"></span>
      {:else}<span class="ok"><Icon name="check" size={12} /></span>{/if}
    </span>
    <span class="name mono">{toolName(tool)}</span>
    <span class="what">{intent(tool)}</span>
    {#if e.files}<span class="stat mono"><span class="plus">+{e.added}</span> <span class="minus">−{e.removed}</span></span>{/if}
    {#if tool.exit_code !== undefined && tool.exit_code !== 0}<span class="chip crit">exit {tool.exit_code}</span>{/if}
    <span class="took muted num">{took}</span>
    {#if hasMore}<span class="chev" class:rot={open}><Icon name="chev" size={12} /></span>{/if}
  </button>
  {#if open}
    <div class="body">
      {#if cmd}<pre class="cmd selectable"><span class="muted">$</span> {cmd}</pre>{/if}
      {#each tool.edits ?? [] as ed}
        <div class="edit">
          <div class="path mono">{ed.path} <span class="plus">+{ed.added}</span> <span class="minus">−{ed.removed}</span>{#if ed.cut}<span class="muted"> {t("· cut")}</span>{/if}</div>
          <DiffLines lines={lineDiff(ed.old, ed.new)} max={200} />
        </div>
      {/each}
      {#if tool.output}<pre class="out selectable">{tool.output}</pre>{/if}
      {#if !cmd && !tool.edits?.length && tool.input}<pre class="out selectable">{JSON.stringify(tool.input, null, 2)}</pre>{/if}
    </div>
  {/if}
</div>

<style>
  .tool { border-radius: var(--r-row); border: 1px solid var(--line-soft); background: var(--card); overflow: hidden; }
  .tool.failed { background: var(--crit-bg); border-color: color-mix(in srgb, var(--crit) 25%, transparent); }
  .head { width: 100%; display: flex; align-items: center; gap: 10px; border: 0; background: transparent; padding: 6px 12px; text-align: left; min-height: 34px; font-size: 12.5px; }
  .head.more:hover { background: var(--hover); }
  .tool.failed .head.more:hover { background: transparent; }
  .st { width: 16px; display: grid; place-items: center; flex: none; }
  .ok { color: var(--good); } .bad { color: var(--crit); }
  .name { color: var(--muted); font-size: 11px; flex: none; min-width: 52px; }
  .what { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: var(--ink); }
  .tool.failed .what { color: var(--crit); }
  .stat { font-size: 12px; flex: none; }
  .plus { color: var(--add-ink); } .minus { color: var(--del-ink); }
  .took { font-size: 11px; flex: none; min-width: 36px; text-align: right; }
  .chev { color: var(--muted); transition: transform .15s; display: grid; } .chev.rot { transform: rotate(90deg); }
  .body { border-top: 1px solid var(--line-soft); padding: 12px 14px; display: grid; gap: 12px; background: var(--page); }
  pre { margin: 0; white-space: pre-wrap; word-break: break-word; font: 11.5px/1.55 var(--mono); max-height: 320px; overflow: auto; }
  .cmd { color: var(--ink); }
  .out { color: var(--ink-2); }
  .edit { border: 1px solid var(--line-soft); border-radius: 12px; overflow: hidden; background: var(--card); }
  .path { padding: 6px 10px; font-size: 12px; border-bottom: 1px solid var(--line-soft); }
</style>
