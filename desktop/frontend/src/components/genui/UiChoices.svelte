<script lang="ts">
  // A question from a reply: the person picks (one option, or several, or writes their own), then
  // sends the answer at once (Send), or puts it into their message being written ("Add to
  // message"), after what is there (one added before stays).
  import { untrack } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import { choiceAnswer, type Choices } from "../../lib/genui";
  import { toDraft, inDraft, sendNow } from "./draft";
  let { c, keep }: { c: Choices; keep: Record<string, unknown> } = $props();

  // As it was left: a mounted block's props do not change (Markdown.svelte mounts it anew).
  // sent: the answer as it was put into the draft (replaced there by the next).
  let picked = $state<string[]>(untrack(() => ((keep.picked as string[] | undefined) ?? []).filter(o => c.options.includes(o))));
  let sent = $state(untrack(() => (keep.sent as string | undefined) ?? ""));
  // The person's own answer: its text, and whether it is picked (for one choice: in place of the options).
  let own = $state(untrack(() => (keep.own as string | undefined) ?? ""));
  let ownOn = $state(untrack(() => !!keep.ownOn));
  let posted = $state(untrack(() => (keep.posted as string | undefined) ?? ""));
  let busy = $state(false);
  function pick(o: string) {
    picked = c.multiple ? (picked.includes(o) ? picked.filter(x => x !== o) : [...picked, o]) : [o];
    if (!c.multiple) ownOn = false;
    keep.picked = $state.snapshot(picked); keep.ownOn = ownOn;
  }
  function pickOwn(on = true) {
    ownOn = c.multiple ? on : true;
    if (!c.multiple) picked = [];
    keep.picked = $state.snapshot(picked); keep.ownOn = ownOn;
  }
  function typed() {
    keep.own = own;
    if (own.trim() && !ownOn) pickOwn();
  }
  // The answer as picked now, in the order the agent gave the options (not the clicks'), the
  // person's own last.
  const chosen = $derived([...c.options.filter(o => picked.includes(o)), ...(ownOn && own.trim() ? [own.trim()] : [])]);
  const now = $derived(chosen.length ? choiceAnswer(c, chosen) : "");
  // What the draft has, read from it: an answer deleted there can be added again.
  const added = $derived(inDraft(sent));
  function answer() {
    if (!now) return;
    toDraft(now, sent);
    sent = keep.sent = now;
  }
  async function send() {
    if (!now || busy) return;
    busy = true;
    const a = now;
    try { if (await sendNow(a)) posted = keep.posted = a; } finally { busy = false; }
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
    <!-- svelte-ignore a11y_click_events_have_key_events -->
    <div class="own" class:on={ownOn} role={c.multiple ? "checkbox" : "radio"} aria-checked={ownOn} tabindex="-1" onclick={() => pickOwn(c.multiple ? !ownOn : true)}>
      <span class="mark" class:box={c.multiple}></span>
      <input type="text" placeholder={t("Your own answer")} bind:value={own} oninput={typed} onclick={e => { e.stopPropagation(); if (!ownOn) pickOwn(); }}
        onkeydown={e => { if (e.key === "Enter" && !e.isComposing && e.keyCode !== 229) { e.preventDefault(); send(); } }} />
    </div>
  </div>
  <div class="foot">
    <button type="button" class="btn primary send" disabled={!now || busy || posted === now} onclick={send}>{t("Send")}</button>
    <button type="button" class="btn ghost add" disabled={!now || inDraft(now)} onclick={answer}>{t("Add to message")}</button>
    {#if posted && posted === now}<span class="muted">{t("Sent")}</span>
    {:else if added}<span class="muted">{t("Added to your message")}</span>{/if}
  </div>
</div>

<style>
  .q { font-weight: 500; margin-bottom: 8px; white-space: pre-wrap; }
  .opts { display: flex; flex-direction: column; gap: 5px; }
  .opt, .own { display: flex; align-items: center; gap: 9px; text-align: left; padding: 7px 11px; border-radius: 10px; border: 1px solid var(--line); background: var(--card); color: var(--ink); font-size: 13px; }
  .opt:hover, .own:hover { background: var(--hover); }
  .opt.on, .own.on { border-color: color-mix(in srgb, var(--accent) 55%, var(--line)); background: var(--accent-bg); }
  .mark { width: 13px; height: 13px; border-radius: 50%; border: 1.5px solid var(--muted); flex: none; box-sizing: border-box; }
  .mark.box { border-radius: 4px; }
  .on .mark { border: 4px solid var(--accent); }
  .on .mark.box { border: 0; background: var(--accent); }
  .own { cursor: text; }
  .own input { flex: 1; min-width: 0; border: 0; background: transparent; color: var(--ink); font: inherit; padding: 0; outline: none; }
  .own input::placeholder { color: var(--muted); }
  .foot { display: flex; align-items: center; gap: 10px; margin-top: 9px; font-size: 12px; }
</style>
