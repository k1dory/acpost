<script lang="ts">
  // A question from a reply: the person picks (one option, or several), and "Add to message" puts
  // the answer into their message being written, after what is there (one added before stays). Nothing is sent
  // from here.
  import { untrack } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import { choiceAnswer, type Choices } from "../../lib/genui";
  import { toDraft, inDraft } from "./draft";
  let { c, keep }: { c: Choices; keep: Record<string, unknown> } = $props();

  // As it was left: a mounted block's props do not change (Markdown.svelte mounts it anew).
  // sent: the answer as it was put into the draft (replaced there by the next).
  let picked = $state<string[]>(untrack(() => ((keep.picked as string[] | undefined) ?? []).filter(o => c.options.includes(o))));
  let sent = $state(untrack(() => (keep.sent as string | undefined) ?? ""));
  function pick(o: string) {
    picked = c.multiple ? (picked.includes(o) ? picked.filter(x => x !== o) : [...picked, o]) : [o];
    keep.picked = $state.snapshot(picked);
  }
  // The answer as picked now, in the order the agent gave the options (not the clicks').
  const now = $derived(picked.length ? choiceAnswer(c, c.options.filter(o => picked.includes(o))) : "");
  // What the draft has, read from it: an answer deleted there can be added again.
  const added = $derived(inDraft(sent));
  function answer() {
    if (!now) return;
    toDraft(now, sent);
    sent = keep.sent = now;
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
  <div class="foot">
    <button type="button" class="btn primary" disabled={!now || inDraft(now)} onclick={answer}>{t("Add to message")}</button>
    {#if added}<span class="muted">{t("Added to your message")}</span>{/if}
  </div>
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
