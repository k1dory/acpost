<script lang="ts">
  import { app, final, type Msg } from "../lib/state.svelte";
  import { who, clock } from "../lib/format";
  import Avatar from "./Avatar.svelte";
  import Working from "./Working.svelte";
  import Markdown from "./Markdown.svelte";
  import TurnBlocks from "./TurnBlocks.svelte";
  import Icon from "./Icon.svelte";
  import { Node, errText } from "../lib/api";

  let { m }: { m: Msg } = $props();
  const turn = $derived.by(() => { app.rev; return app.turns[m.id]?.view(); });
  const done = $derived(final(m.state));
  const streamed = $derived(turn ? turn.blocks.filter(b => b.t === "text").map(b => (b as any).text).join("") : "");
  const sender = $derived(who(m.from));
  const agent = $derived(who(m.to));
  // The reply is the authoritative text once the turn is over: shown when the stream differs.
  // A queued message: its text can still change, or it can be taken back.
  let editing = $state<string | null>(null);
  let saving = $state(false);
  async function saveEdit() {
    const text = editing;
    if (saving || text === null || !text.trim()) return;
    saving = true;
    try { m.body = await Node.EditQueued(m.id, text); editing = null; } catch (e) { app.error = errText(e); editing = null; }
    saving = false;
  }
  async function takeBack() {
    try { await Node.Cancel(m.id); } catch (e) { app.error = errText(e); }
  }
  const replyDiffers = $derived(done && !!m.reply && m.reply.trim() !== streamed.trim() && !streamed.trim().endsWith(m.reply.trim()));
</script>

<div class="pair" class:focus={app.focus === m.id}>
  <div class="user">
    <div class="meta"><Avatar addr={m.from} size={20} /><span class="nm">{sender.name}</span><span class="muted num">{clock(m.created_at)}</span>{#if m.depth > 0}<span class="chip">subagent</span>{/if}</div>
    {#if editing !== null}
      <textarea class="ubody edit" bind:value={editing} readonly={saving} rows="3"
        onkeydown={e => { if (e.key === "Enter" && (e.metaKey || !e.shiftKey)) { e.preventDefault(); saveEdit(); } else if (e.key === "Escape" && !saving) editing = null; }}></textarea>
      <div class="eacts"><button class="btn sm" disabled={saving} onclick={() => (editing = null)}>Cancel</button><button class="btn primary sm" disabled={saving} onclick={saveEdit}>Save</button></div>
    {:else}
      <div class="ubody selectable">{m.body}</div>
      {#if m.state === "queued"}
        <div class="eacts"><button class="btn ghost sm" onclick={() => (editing = m.body ?? "")}>Edit</button><button class="btn ghost sm" onclick={takeBack}>Take back</button></div>
      {/if}
    {/if}
    {#if m.files?.filter(f => !f.reply).length}
      <div class="files">{#each m.files.filter(f => !f.reply) as f}<span class="chip"><Icon name="file" size={12} /> {f.name}</span>{/each}</div>
    {/if}
  </div>

  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="agent" onclick={() => (app.focus = m.id)}>
    <div class="meta">
      <Avatar addr={m.to} size={20} busy={m.state === "running"} brand={m.to.startsWith("agent:sb_") ? app.chat?.cloud_agent ?? "" : ""} /><span class="nm">{agent.name}</span>{#if agent.node}<span class="muted">@{agent.node}</span>{/if}
      {#if m.state === "running"}<Working agent={m.to} view={turn} />
      {:else if m.state === "queued"}<span class="chip warn">queued</span>
      {:else if m.state === "failed"}<span class="chip crit">failed</span>
      {:else if m.state === "cancelled"}<span class="chip">cancelled</span>{/if}
    </div>

    {#if turn}<TurnBlocks view={turn} msg={m.id} {done} agent={m.to} />{/if}

    {#if replyDiffers || (done && !streamed && m.reply)}
      <Markdown text={m.reply ?? ""} />
    {/if}
    {#if !done && !streamed && !(turn?.blocks.length)}
      <div class="wait"><span class="sk" style="width:60%"></span><span class="sk" style="width:40%"></span></div>
    {/if}
    {#if m.state === "failed" && m.error}
      <div class="err selectable"><b>{m.error_code ?? "error"}</b> — {m.error}{#if m.hint}<div class="muted">{m.hint}</div>{/if}</div>
    {/if}
    {#if !done}
      <div class="acts"><button class="btn ghost sm" onclick={() => app.cancel(m.id)}><Icon name="stop" size={12} /> Stop</button></div>
    {/if}
  </div>
</div>

<style>
  .pair { display: grid; gap: 18px; padding: 4px 0 32px; }
  .meta { display: flex; align-items: center; gap: 8px; font-size: 12.5px; margin-bottom: 10px; flex-wrap: wrap; }
  .nm { font-weight: 600; }
  .user { justify-self: end; max-width: 85%; }
  .edit { width: 100%; min-width: 360px; resize: vertical; font: inherit; border: 1px solid var(--accent); outline: 0; }
  .eacts { display: flex; gap: 6px; justify-content: flex-end; margin-top: 6px; }
  .user .meta { justify-content: flex-end; }
  .ubody { white-space: pre-wrap; background: var(--card); border: 1px solid var(--line); border-radius: 16px 16px 5px 16px; padding: 8px 13px; box-shadow: inset 0 1px 0 var(--edge), var(--shadow); }
  .files { display: flex; gap: 6px; justify-content: flex-end; margin-top: 6px; flex-wrap: wrap; }
  .agent { min-width: 0; display: grid; gap: 8px; }
  .agent > .meta { margin-bottom: 2px; }
  .err { padding: 10px 12px; border-radius: var(--r-row); background: var(--crit-bg); color: var(--crit); font-size: 13px; }
  .wait { display: grid; gap: 8px; } .wait .sk { height: 12px; }
  .acts { margin-top: 2px; }
</style>
