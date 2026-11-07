<script lang="ts">
  import { app } from "../lib/state.svelte";
  import type { Turn } from "../lib/turn";
  import Markdown from "./Markdown.svelte";
  import ToolRow from "./ToolRow.svelte";
  import Approval from "./Approval.svelte";
  import Thought from "./Thought.svelte";
  import Plan from "./Plan.svelte";
  import Icon from "./Icon.svelte";

  // A turn's blocks as the agent did them: text, thinking, tool calls, requests, plan, notices.
  let { view, msg = "", done, agent = "" }: { view: ReturnType<Turn["view"]>; msg?: string; done: boolean; agent?: string } = $props();
</script>

{#each view.blocks as b, i}
  {#if b.t === "text"}
    <Markdown text={b.text} />
  {:else if b.t === "thought"}
    <Thought text={b.text} ms={b.end - b.start} live={!done && i === view.blocks.length - 1} {agent} />
  {:else if b.t === "tool"}
    {@const t = view.tools.get(b.id)}
    {#if t}<ToolRow tool={t} final={done} />{/if}
  {:else if b.t === "request"}
    {@const r = view.requests.get(b.id)}
    {#if r}<Approval req={r} msg={msg} />{/if}
  {:else if b.t === "plan"}
    <Plan entries={b.entries} />
  {:else if b.t === "notice"}
    <div class="notice" class:warnn={b.severity === "warning" || b.severity === "error"}>{b.text}</div>
  {:else if b.t === "changes"}
    <button class="btn sm" onclick={() => { app.focus = msg; app.tab = "changes"; app.panel = true; }}><Icon name="diff" size={13} /> Changed files</button>
  {/if}
{/each}

<style>
  .notice { font-size: 12px; color: var(--muted); }
  .notice.warnn { color: var(--warn); }
</style>
