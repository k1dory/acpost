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
  // Calls made one after another are one list (a card, a line between its rows), not a card each:
  // a turn of twenty reads is one thing to look past. `i` is the block's place in the turn.
  type Block = (typeof view.blocks)[number];
  type Part = { i: number; b: Block; tools?: string[] };
  const parts = $derived.by(() => {
    const out: Part[] = [];
    view.blocks.forEach((b, i) => {
      const last = out[out.length - 1];
      if (b.t === "tool" && !view.tools.get(b.id)) return; // a call not known: no row, and no break in the list
      if (b.t === "tool" && last?.tools) last.tools.push(b.id);
      else out.push(b.t === "tool" ? { i, b, tools: [b.id] } : { i, b });
    });
    return out;
  });
</script>

{#each parts as { b, i, tools } (i)}
  {#if b.t === "text"}
    <Markdown text={b.text} scope={scope ? scope + ":" + i : ""} />
  {:else if b.t === "thought"}
    <Thought text={b.text} ms={b.end - b.start} live={!done && i === view.blocks.length - 1} {agent} />
  {:else if tools}
    <div class="series">{#each tools as id (id)}{@const tool = view.tools.get(id)}{#if tool}<ToolRow {tool} {msgState} {agent} />{/if}{/each}</div>
  {:else if b.t === "request"}
    {@const r = view.requests.get(b.id)}
    {#if r}<Approval req={r} msg={msg} />{/if}
  {:else if b.t === "plan"}
    <Plan entries={b.entries} />
  {:else if b.t === "notice"}
    <div class="notice" class:warnn={b.severity === "warning" || b.severity === "error"}>{b.text}</div>
  {:else if b.t === "changes"}
    <!-- (Beside the IDE there is no panel: the chat's own view is shown with it, as for the terminal.) -->
    <button class="btn sm changes" onclick={() => { app.focus = msg; if (app.beside) app.view = "chat"; app.openTab("changes"); }}><Icon name="diff" size={13} /> {t("Changed files")}</button>
  {/if}
{/each}

<style>
  .changes { justify-self: start; }
  .series { display: grid; grid-template-columns: minmax(0, 1fr); min-width: 0; }
  .series > :global(.tool) { border-radius: 0; }
  .series > :global(.tool + .tool) { border-top: 0; }
  .series > :global(.tool:first-child) { border-top-left-radius: var(--r-row); border-top-right-radius: var(--r-row); }
  .series > :global(.tool:last-child) { border-bottom-left-radius: var(--r-row); border-bottom-right-radius: var(--r-row); }
  .notice { font-size: 12px; color: var(--muted); }
  .notice.warnn { color: var(--warn-text); }
</style>
