<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { intent, toolName, toolState, editTotals, lineDiff, type Tool } from "../lib/turn";
  import { dur } from "../lib/format";
  import { app } from "../lib/state.svelte";
  import { revealOf, localFiles } from "../lib/ide";
  import Icon from "./Icon.svelte";
  import DiffLines from "./DiffLines.svelte";

  // agent: whose call it is (its files are shown in the IDE only when they are this Mac's).
  let { tool, msgState, agent = "" }: { tool: Tool; msgState: string; agent?: string } = $props();
  // Beside the IDE (or a module) there is no panel of changed files: an edit shows its diff in the
  // chat as it is made, its first lines; the person folds or unfolds any row as they like.
  let asked = $state<boolean | null>(null);
  const shown = $derived(app.beside && !!tool.edits?.length);
  const open = $derived(asked ?? shown);
  const st = $derived(toolState(tool, msgState));
  const e = $derived(editTotals(tool));
  // A replayed history has no real timing: no duration then.
  const took = $derived(tool.ended && tool.ended - tool.started >= 5 ? dur(tool.ended - tool.started) : "");
  const cmd = $derived(typeof tool.input?.command === "string" ? tool.input.command : Array.isArray(tool.input?.command) ? tool.input.command.join(" ") : "");
  const hasMore = $derived(!!(tool.output || tool.edits?.length || tool.input));
</script>

<div class="tool" class:failed={st === "failed"}>
  <button class="head" onclick={() => hasMore && (asked = !open)} class:more={hasMore} aria-expanded={hasMore ? open : undefined}>
    <span class="st">
      {#if st === "running"}<span class="spin"></span>
      {:else if st === "failed"}<span class="bad"><Icon name="x" size={12} /></span>
      {:else if st === "pending"}<span class="dot queued"></span>
      {:else if st === "cancelled" || st === "expired" || st === "unknown"}<span class="muted" title={t(st === "cancelled" ? "Cancelled before it finished" : st === "expired" ? "Expired before it finished" : "Its result is not known")}><Icon name="x" size={12} /></span>
      {:else}<span class="ok"><Icon name="check" size={12} /></span>{/if}
    </span>
    <span class="name mono">{toolName(tool)}</span>
    <span class="what">{intent(tool)}</span>
    {#if e.files}<span class="stat mono"><span class="plus">+{e.added}</span> <span class="minus">−{e.removed}</span></span>{/if}
    {#if tool.exit_code !== undefined && tool.exit_code !== 0}<span class="chip crit">{t("exit {code}", { code: tool.exit_code })}</span>{/if}
    {#if took}<span class="took muted num">{took}</span>{/if}
    {#if hasMore}<span class="chev" class:rot={open}><Icon name="chev" size={12} /></span>{/if}
  </button>
  {#if open}
    <div class="body">
      {#if cmd}<pre class="cmd selectable"><span class="muted">$</span> {cmd}</pre>{/if}
      {#each tool.edits ?? [] as ed}
        <div class="edit">
          <div class="path mono">
            <!-- Beside the IDE: the file opens there, with this edit as a diff. -->
            {#if app.view === "ide" && localFiles(agent)}<button class="open mono" title={t("Show in the IDE")} onclick={() => app.revealInIDE(revealOf(ed), agent, true)}>{ed.path}</button>{:else}{ed.path}{/if}
            <span class="plus">+{ed.added}</span> <span class="minus">−{ed.removed}</span>{#if ed.cut}<span class="muted"> {t("· cut")}</span>{/if}</div>
          <DiffLines lines={lineDiff(ed.old, ed.new)} max={asked ? 200 : 12} />
        </div>
      {/each}
      {#if tool.output}<pre class="out selectable">{tool.output}</pre>{/if}
      {#if !cmd && !tool.edits?.length && tool.input}<pre class="out selectable">{JSON.stringify(tool.input, null, 2)}</pre>{/if}
    </div>
  {/if}
</div>

<style>
  .open { border: 0; background: transparent; padding: 0; color: inherit; font-size: inherit; text-decoration: underline; text-decoration-color: var(--line); text-underline-offset: 3px; cursor: pointer; }
  .open:hover { text-decoration-color: currentColor; }
  .tool { border-radius: var(--r-row); border: 1px solid var(--line-soft); background: var(--card); overflow: hidden; container-type: inline-size; }
  .head:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .tool.failed { background: var(--crit-bg); border-color: color-mix(in srgb, var(--crit) 25%, transparent); }
  .head { width: 100%; display: flex; align-items: center; gap: 10px; border: 0; background: transparent; padding: 6px 12px; text-align: left; min-height: 34px; font-size: 12.5px; }
  .head.more:hover { background: var(--hover); }
  .tool.failed .head.more:hover { background: transparent; }
  .st { width: 16px; display: grid; place-items: center; flex: none; }
  /* A call that went well is the usual case: its mark is quiet, a failed one stands out. */
  .ok { color: var(--muted); } .bad { color: var(--crit); }
  .name { color: var(--muted); font-size: 11px; flex: none; min-width: 52px; }
  .what { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: var(--ink); }
  .tool.failed .what { color: var(--crit); }
  .stat { font-size: 12px; flex: none; }
  .plus { color: var(--add-ink); } .minus { color: var(--del-ink); }
  .took { font-size: 11px; flex: none; min-width: 36px; text-align: right; }
  .chev { color: var(--muted); transition: transform .15s; display: grid; } .chev.rot { transform: rotate(90deg); }
  .body { border-top: 1px solid var(--line-soft); padding: 12px 14px; display: grid; gap: 12px; background: var(--page); }
  pre { margin: 0; white-space: pre-wrap; word-break: break-word; font: 12px/1.55 var(--mono); max-height: 320px; overflow: auto; }
  .cmd { color: var(--ink); }
  .out { color: var(--ink-2); }
  .edit { border: 1px solid var(--line-soft); border-radius: 12px; overflow: hidden; background: var(--card); }
  .path { padding: 6px 10px; font-size: 12px; border-bottom: 1px solid var(--line-soft); overflow-wrap: anywhere; }
  /* A narrow column: what the call is for keeps the width; an edit's diff reaches the card's sides. */
  @container (max-width: 400px) {
    .head { gap: 8px; padding: 6px 10px; }
    .name { min-width: 0; }
    .took { display: none; }
    .body { padding: 8px 0; gap: 8px; }
    .body > pre { padding: 0 10px; }
    .edit { border-width: 1px 0; border-radius: 0; }
  }
</style>
