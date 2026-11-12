<script lang="ts">
  import { t, tr } from "../lib/i18n.svelte";
  // Why a turn failed, said to a person (the node's code; its message is the detail).
  const failTitles: Record<string, string> = {
    agent_error: "The agent stopped with an error",
    turn_timeout: "The turn took too long",
    agent_stopped: "The agent was stopped",
    agent_crashed: "The agent's process crashed",
    agent_unavailable: "The agent is not available",
    agent_start: "The agent did not start",
    agent_auth: "The agent needs you to sign in",
    session_not_found: "That session no longer exists",
    session_busy: "The session is open in another app",
    expired: "No agent took it in time",
    refused: "Not allowed",
    conflict: "It changed meanwhile",
    unsupported: "Not supported here",
  };
  import { app, final, type Msg } from "../lib/state.svelte";
  import { who, clock } from "../lib/format";
  import Avatar from "./Avatar.svelte";
  import Working from "./Working.svelte";
  import Subtasks from "./Subtasks.svelte";
  import Markdown from "./Markdown.svelte";
  import TurnBlocks from "./TurnBlocks.svelte";
  import Icon from "./Icon.svelte";
  import { Node, errText } from "../lib/api";
  import { bytes } from "../lib/names";
  import { openTrace } from "../lib/analytics.svelte";

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
    <div class="meta"><Avatar addr={m.from} size={20} /><span class="nm">{sender.name}</span><span class="muted num">{clock(m.created_at)}</span>{#if m.depth > 0}<span class="chip">{t("subagent")}</span>{/if}</div>
    {#if editing !== null}
      <textarea class="ubody edit" bind:value={editing} readonly={saving} rows="3"
        onkeydown={e => { if (e.key === "Enter" && (e.metaKey || !e.shiftKey)) { e.preventDefault(); saveEdit(); } else if (e.key === "Escape" && !saving) editing = null; }}></textarea>
      <div class="eacts"><button class="btn sm" disabled={saving} onclick={() => (editing = null)}>{t("Cancel")}</button><button class="btn primary sm" disabled={saving} onclick={saveEdit}>{t("Save")}</button></div>
    {:else}
      {#if m.body}<div class="ubody selectable">{m.body}</div>{/if}
      {#if m.state === "queued"}
        <div class="eacts"><button class="btn ghost sm" onclick={() => (editing = m.body ?? "")}>{t("Edit")}</button><button class="btn ghost sm" onclick={takeBack}>{t("Take back")}</button></div>
      {/if}
    {/if}
    {#if m.files?.filter(f => !f.reply).length}
      <div class="files">{#each m.files.filter(f => !f.reply) as f}{#if f.size > 0 && !(f as any).uri}<button class="chip fbtn" onclick={() => app.openArtifact({ msg: m.id, name: f.name })} title={f.name}><Icon name="file" size={12} /> {f.name}</button>{:else}<span class="chip"><Icon name="file" size={12} /> {f.name}</span>{/if}{/each}</div>
    {/if}
  </div>

  <!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
  <div class="agent" onclick={() => (app.focus = m.id)}>
    <div class="meta">
      <Avatar addr={m.to} size={20} busy={m.state === "running"} brand={m.to.startsWith("agent:sb_") ? app.chat?.cloud_agent ?? "" : ""} /><span class="nm">{agent.name}</span>{#if agent.node}<span class="muted">@{agent.node}</span>{/if}
      {#if m.state === "running"}<Working agent={m.to} view={turn} msg={m.id} />
      {:else if m.state === "queued"}<span class="chip warn">{t("queued")}</span>
      {:else if m.state === "failed"}<span class="chip crit">{t("failed")}</span>
      {:else if m.state === "cancelled"}<span class="chip">{t("cancelled")}</span>
      {:else if m.state === "expired"}<span class="chip warn">{t("expired")}</span>{/if}
      {#if m.state !== "queued" && m.to.startsWith("agent:")}<button class="btn ghost sm trace" title={t("Where this turn's time went")} onclick={e => { e.stopPropagation(); openTrace(m.thread || m.id, m.id); }}><Icon name="activity" size={12} /> {t("Trace")}</button>{/if}
    </div>

    {#if turn}<TurnBlocks view={turn} msg={m.id} msgState={m.state} agent={m.to} />{/if}
    {#if m.files?.some(f => f.reply)}
      <!-- What the agent handed back: each opens in the artifact viewer. -->
      <div class="arts">
        {#each m.files.filter(f => f.reply && f.size > 0 && !(f as any).uri) as f}
          <button class="art" onclick={() => app.openArtifact({ msg: m.id, name: f.name })} title={f.name}>
            <Icon name="file" size={13} /><span class="an">{f.name}</span><span class="muted as">{bytes(f.size)}</span>
          </button>
        {/each}
      </div>
    {/if}
    <Subtasks msg={m.id} running={m.state === "running"} />

    {#if replyDiffers || (done && !streamed && m.reply)}
      <Markdown text={m.reply ?? ""} />
    {/if}
    {#if !done && !streamed && !(turn?.blocks.length)}
      <div class="wait"><span class="sk" style="width:60%"></span><span class="sk" style="width:40%"></span></div>
    {/if}
    {#if app.broken[m.id]}
      <!-- The stream stopped (or the events could not be read): what is shown may be behind. -->
      <div class="err selectable"><b>{t("This turn is not being followed")}</b><div>{app.broken[m.id]}</div>
        <div><button class="btn sm" onclick={() => app.followAgain(m)}>{t("Retry")}</button></div></div>
    {/if}
    {#if m.state === "failed" && m.error}
      <div class="err selectable"><b>{t(failTitles[m.error_code ?? ""] ?? "The turn failed")}</b><div>{tr(m.error ?? "")}</div>{#if m.hint}<div class="hint">{t("What to do")}: {tr(m.hint)}</div>{/if}</div>
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
  .arts { display: flex; gap: 6px; flex-wrap: wrap; margin-top: 8px; }
  .art { display: inline-flex; align-items: center; gap: 7px; border: 1px solid var(--line-soft); background: var(--card); border-radius: 10px; padding: 7px 10px; font-size: 12.5px; max-width: 280px; }
  .art:hover { border-color: var(--accent); }
  .an { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
  .as { font-size: 11px; flex: none; }
  .fbtn { cursor: default; }
  .fbtn:hover { border-color: var(--accent); }
  .files { display: flex; gap: 6px; justify-content: flex-end; margin-top: 6px; flex-wrap: wrap; }
  .agent { min-width: 0; display: grid; gap: 8px; }
  .agent > .meta { margin-bottom: 2px; }
  .trace { margin-left: auto; gap: 4px; color: var(--muted); opacity: 0; }
  .agent:hover .trace, .trace:focus-visible { opacity: 1; }
  .err .hint { margin-top: 6px; opacity: .85; }
  .err { display: grid; gap: 2px; padding: 10px 12px; border-radius: var(--r-row); background: var(--crit-bg); color: var(--crit); font-size: 13px; }
  .wait { display: grid; gap: 8px; } .wait .sk { height: 12px; }
</style>
