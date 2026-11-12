<script lang="ts">
  // A question from a reply: the person picks, and the answer goes into their message being
  // written (the composer's draft, as ⌘L puts text there). Nothing is sent from here.
  import { untrack } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import { choiceAnswer, type Choices } from "../../lib/genui";
  import { toDraft } from "./draft";
  let { c, keep }: { c: Choices; keep: Record<string, unknown> } = $props();

  // As it was left: a mounted block's props do not change (Markdown.svelte mounts it anew).
  let picked = $state<string[]>(untrack(() => ((keep.picked as string[] | undefined) ?? []).filter(o => c.options.includes(o))));
  let added = $state(untrack(() => !!keep.added));
  function pick(o: string) {
    picked = c.multiple ? (picked.includes(o) ? picked.filter(x => x !== o) : [...picked, o]) : [o];
    keep.picked = $state.snapshot(picked);
    added = keep.added = false;
    if (!c.multiple) answer();
  }
  function answer() {
    if (!picked.length) return;
    // In the order the agent gave them, not the order of the clicks.
    toDraft(choiceAnswer(c, c.options.filter(o => picked.includes(o))));
    added = keep.added = true;
  }
</script>

<div class="ask">
  <div class="q">{c.question}</div>
  <div class="opts" role={c.multiple ? "group" : "radiogroup"} aria-label={c.question}>
    {#each c.options as o}
      <button type="button" class="opt" class:on={picked.includes(o)} role={c.multiple ? "checkbox" : "radio"} aria-checked={picked.includes(o)} onclick={() => pick(o)}>
        <span class="mark" class:box={c.multiple}></span>{o}
      </button>
    {/each}
  </div>
  {#if c.multiple}
    <div class="foot">
      <button type="button" class="btn primary" disabled={!picked.length} onclick={answer}>{t("Add to message")}</button>
      {#if added}<span class="muted">{t("Added to your message")}</span>{/if}
    </div>
  {:else if added}
    <div class="foot"><span class="muted">{t("Added to your message")}</span></div>
  {/if}
</div>

<style>
  .q { font-weight: 500; margin-bottom: 8px; white-space: pre-wrap; }
  .opts { display: flex; flex-direction: column; gap: 5px; }
  .opt { display: flex; align-items: center; gap: 9px; text-align: left; padding: 7px 11px; border-radius: 10px; border: 1px solid var(--line); background: var(--card); color: var(--ink); font-size: 13px; }
  .opt:hover { background: var(--hover); }
  .opt.on { border-color: color-mix(in srgb, var(--accent) 55%, var(--line)); background: var(--accent-bg); }
  .mark { width: 13px; height: 13px; border-radius: 50%; border: 1.5px solid var(--muted); flex: none; box-sizing: border-box; }
  .mark.box { border-radius: 4px; }
  .on .mark { border: 4px solid var(--accent); }
  .on .mark.box { border: 0; background: var(--accent); }
  .foot { display: flex; align-items: center; gap: 10px; margin-top: 9px; font-size: 12px; }
</style>
