<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app, final } from "../lib/state.svelte";
  import type { Turn } from "../lib/turn";
  import Markdown from "./Markdown.svelte";
  import ToolRow from "./ToolRow.svelte";
  import Approval from "./Approval.svelte";
  import Thought from "./Thought.svelte";
  import Plan from "./Plan.svelte";
  import Icon from "./Icon.svelte";

  // A turn's blocks as the agent did them: text, thinking, tool calls, requests, plan, notices.
  // msgState: the message's (a replayed history is "done"). scope: what names the turn, for what a
  // person did to the components in its text to be kept (the message's id when it has one).
  let { view, msg = "", msgState, agent = "", scope = msg }: { view: ReturnType<Turn["view"]>; msg?: string; msgState: string; agent?: string; scope?: string } = $props();
  const done = $derived(final(msgState));
</script>

{#each view.blocks as b, i}
  {#if b.t === "text"}
    <Markdown text={b.text} scope={scope ? scope + ":" + i : ""} />
  {:else if b.t === "thought"}
    <Thought text={b.text} ms={b.end - b.start} live={!done && i === view.blocks.length - 1} {agent} />
  {:else if b.t === "tool"}
    {@const t = view.tools.get(b.id)}
    {#if t}<ToolRow tool={t} {msgState} {agent} />{/if}
  {:else if b.t === "request"}
    {@const r = view.requests.get(b.id)}
    {#if r}<Approval req={r} msg={msg} />{/if}
  {:else if b.t === "plan"}
    <Plan entries={b.entries} />
  {:else if b.t === "notice"}
    <div class="notice" class:warnn={b.severity === "warning" || b.severity === "error"}>{b.text}</div>
  {:else if b.t === "changes"}
    <button class="btn sm" onclick={() => { app.focus = msg; app.openTab("changes"); }}><Icon name="diff" size={13} /> {t("Changed files")}</button>
  {/if}
{/each}

<style>
  .notice { font-size: 12px; color: var(--muted); }
  .notice.warnn { color: var(--warn); }
</style>
