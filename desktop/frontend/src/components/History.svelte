<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { who } from "../lib/format";
  import Avatar from "./Avatar.svelte";
  import TurnBlocks from "./TurnBlocks.svelte";

  // A conversation started elsewhere (a terminal's Claude Code, Codex, a cloud sandbox), read back.
  const agent = $derived(app.chat?.agent ?? "");
  const views = $derived(app.history.map(s => ({ user: s.user, view: s.turn.view() })));
</script>

{#if app.historyLoading}
  <div class="wait"><span class="sk" style="width:55%"></span><span class="sk" style="width:35%"></span><span class="sk" style="width:65%"></span></div>
{:else}
  {#each views as s, i (i)}
    <div class="pair">
      {#if s.user.trim()}
        <div class="user"><div class="ubody selectable">{s.user.trim()}</div></div>
      {/if}
      {#if s.view.blocks.length}
        <div class="agent">
          <div class="meta"><Avatar addr={agent} size={20} brand={app.chat?.cloud_agent ?? ""} /><span class="nm">{who(agent).name}</span></div>
          <TurnBlocks view={s.view} done={true} {agent} />
        </div>
      {/if}
    </div>
  {/each}
  {#if views.length}<div class="sep"><span>earlier, outside acpost</span></div>{/if}
{/if}

<style>
  .pair { display: grid; gap: 18px; padding: 4px 0 28px; }
  .user { justify-self: end; max-width: 85%; }
  .ubody { white-space: pre-wrap; background: var(--card); border: 1px solid var(--line); border-radius: 16px 16px 5px 16px; padding: 8px 13px; box-shadow: inset 0 1px 0 var(--edge), var(--shadow); max-height: 320px; overflow: auto; }
  .agent { min-width: 0; display: grid; gap: 8px; }
  .meta { display: flex; align-items: center; gap: 8px; font-size: 12.5px; }
  .nm { font-weight: 600; }
  .wait { display: grid; gap: 10px; padding: 8px 0; } .wait .sk { height: 12px; }
  .sep { display: flex; align-items: center; gap: 10px; color: var(--muted); font-size: 11px; margin: 0 0 24px; }
  .sep::before, .sep::after { content: ""; flex: 1; height: 1px; background: var(--line-soft); }
</style>
