<script lang="ts">
  import { t, tr } from "../lib/i18n.svelte";
  // Why a turn failed, said to a person (the node's code; its message is the detail).
  const failTitles: Record<string, string> = {
    agent_error: "The agent stopped with an error",
    turn_timeout: "The turn hit its time limit",
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
  // What a person reads for a failure the app can say in its own words (the node's detail is
  // written for its log: config keys, Go durations); else the node's text.
  const failSaid: Record<string, { detail: string; hint: string }> = {
    turn_timeout: {
      detail: "The agent worked longer than the limit set for it in the node's settings, and the node stopped the turn.",
      hint: "Write “continue”: the agent picks up where it stopped.",
    },
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
  import { shellNote } from "../lib/shell";
  import ShellNote from "./ShellNote.svelte";

  let { m }: { m: Msg } = $props();
  const turn = $derived.by(() => { app.rev; return app.turns[m.id]?.view(); });
  const done = $derived(final(m.state));
  // A command the person ran, told to the agent: shown as the command, its output folded.
  const shell = $derived(shellNote(m.body));
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
  // A message waiting behind a turn of the same agent in this chat: "Send now" stops that turn,
  // and the agent takes this one at once (the node starts what waits once a turn ends).
  // Only the next of the agent's queue: the node gives the agent its oldest waiting message, so
  // stopping the turn for a later one would start another (the one before it) instead.
  // The node counts the agent's queue across every chat: one waiting, it is this one.
  const next = $derived(m.state === "queued" && (app.agents.find(a => a.address === m.to)?.queued ?? 0) <= 1
    && !app.msgs.some(x => x.state === "queued" && x.to === m.to && x.id !== m.id));
  const ahead = $derived(next ? app.msgs.filter(x => x.state === "running" && x.to === m.to) : []);
  let hurrying = $state(false);
  async function sendNow() {
    hurrying = true;
    try { await Promise.all(ahead.map(x => app.cancel(x.id))); } finally { hurrying = false; }
  }
  async function takeBack() {
    try { await Node.Cancel(m.id); } catch (e) { app.error = errText(e); }
  }
  // The person's own message says only when: whose it is is plain from its side of the chat. Another
  // person's (a shared chat) and a subagent's say who.
  const own = $derived(!m.depth && m.from.startsWith("user:") && !m.from.includes("@"));
  const replyDiffers = $derived(done && !!m.reply && m.reply.trim() !== streamed.trim() && !streamed.trim().endsWith(m.reply.trim()));
</script>

<div class="pair" class:focus={app.focus === m.id && app.panel && !app.beside && app.tab === "changes"}>
  <div class="user" class:editing={editing !== null} class:shell={!!shell}>
    <div class="meta" class:own>{#if !own}<Avatar addr={m.from} size={20} /><span class="nm">{sender.name}</span>{/if}<span class="muted num">{clock(m.created_at)}</span>{#if m.depth > 0}<span class="chip">{t("subagent")}</span>{/if}</div>
    {#if editing !== null}
      <textarea class="ubody edit" bind:value={editing} readonly={saving} rows="3"
        onkeydown={e => { if (e.key === "Enter" && (e.metaKey || !e.shiftKey)) { e.preventDefault(); saveEdit(); } else if (e.key === "Escape" && !saving) editing = null; }}></textarea>
      <div class="eacts"><button class="btn sm" disabled={saving} onclick={() => (editing = null)}>{t("Cancel")}</button><button class="btn primary sm" disabled={saving} onclick={saveEdit}>{t("Save")}</button></div>
    {:else}
      {#if shell}<ShellNote note={shell} />{:else if m.body}<div class="ubody selectable">{m.body}</div>{/if}
      {#if m.state === "queued"}
        <div class="eacts">
          {#if ahead.length}<span class="muted wait">{t("Waits for the agent's turn to end")}</span>
            <button class="btn ghost sm now" disabled={hurrying} title={t("Stops the agent's turn now: it gets this message at once")} onclick={sendNow}>{t("Send now")}</button>{/if}
          <button class="btn ghost sm" onclick={() => (editing = m.body ?? "")}>{t("Edit")}</button><button class="btn ghost sm" onclick={takeBack}>{t("Take back")}</button>
        </div>
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
      {#if m.state !== "queued" && m.to.startsWith("agent:")}<button class="btn ghost sm trace" title={t("Where this turn's time went")} aria-label={t("Trace")} onclick={e => { e.stopPropagation(); openTrace(m.thread || m.id, m.id); }}><Icon name="activity" size={13} /></button>{/if}
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
      <Markdown text={m.reply ?? ""} scope={m.id + ":reply"} />
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
      {@const said = failSaid[m.error_code ?? ""]}<div class="err selectable"><b>{t(failTitles[m.error_code ?? ""] ?? "The turn failed")}</b><div>{said ? t(said.detail) : tr(m.error ?? "")}</div>{#if said || m.hint}<div class="hint">{t("What to do")}: {said ? t(said.hint) : tr(m.hint ?? "")}</div>{/if}</div>
    {/if}
  </div>
</div>

<style>
  .eacts .wait { font-size: 11.5px; margin-right: auto; align-self: center; }
  /* One column no wider than the chat: a long line in it (a command, an approval's buttons) wraps
     or is cut there, it does not push the column past a narrow window. */
  .pair { position: relative; display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; padding: 2px 0 22px; }
  /* The turn whose changed files the panel shows. */
  .pair.focus > .agent::before { content: ""; position: absolute; left: -12px; top: 0; bottom: 0; width: 2px; border-radius: 2px; background: color-mix(in srgb, var(--accent) 55%, transparent); }
  .meta { display: flex; align-items: center; gap: 8px; font-size: 12.5px; margin-bottom: 8px; flex-wrap: wrap; }
  .meta.own { font-size: 11px; margin-bottom: 4px; }
  .nm { font-weight: 600; }
  .user { justify-self: end; max-width: 85%; }
  /* A command's line is the column's width: its output, unfolded, has the room a terminal gives. */
  .user.shell { justify-self: stretch; max-width: none; min-width: 0; }
  /* The editor is 360px wide where there is room (the bubble sizes itself to it), the column's
     width where there is not. */
  .user.editing { width: 360px; }
  .ubody.edit { max-height: none; }
  .edit { width: 100%; min-width: 0; resize: vertical; font: inherit; border: 1px solid var(--accent); outline: 0; }
  .eacts { display: flex; gap: 6px; justify-content: flex-end; margin-top: 6px; }
  .user .meta { justify-content: flex-end; }
  /* A pasted path or a stack's line wraps inside the bubble; a long paste scrolls in it. */
  .ubody { font-size: 13.5px; white-space: pre-wrap; overflow-wrap: anywhere; max-height: 320px; overflow: auto; background: var(--card); border: 1px solid var(--line); border-radius: 16px 16px 5px 16px; padding: 8px 13px; box-shadow: inset 0 1px 0 var(--edge), var(--shadow); }
  .arts { display: flex; gap: 6px; flex-wrap: wrap; margin-top: 8px; }
  .art { display: inline-flex; align-items: center; gap: 7px; border: 1px solid var(--line-soft); background: var(--card); border-radius: 10px; padding: 7px 10px; font-size: 12.5px; max-width: 280px; }
  .art:hover { border-color: var(--accent); }
  .an { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
  .as { font-size: 11px; flex: none; }
  .fbtn { cursor: default; }
  .fbtn:hover { border-color: var(--accent); }
  .files { display: flex; gap: 6px; justify-content: flex-end; margin-top: 6px; flex-wrap: wrap; }
  .agent { position: relative; min-width: 0; display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; }
  /* One line of one height whatever the turn's state is: what the agent is doing is cut, it does not
     wrap under the name and move the text below. */
  .agent > .meta { margin-bottom: 2px; flex-wrap: nowrap; min-height: 24px; min-width: 0; }
  .agent > .meta > :global(*) { flex: none; }
  .agent > .meta > :global(.working) { flex: 0 1 auto; min-width: 0; overflow: hidden; }
  .trace { margin-left: auto; width: 24px; padding: 0; justify-content: center; color: var(--muted); opacity: 0; }
  .agent:hover .trace, .trace:focus-visible { opacity: 1; }
  .err .hint { margin-top: 6px; opacity: .85; }
  .err { display: grid; grid-template-columns: minmax(0, 1fr); overflow-wrap: anywhere; gap: 2px; padding: 10px 12px; border-radius: var(--r-row); background: var(--crit-bg); color: var(--crit); font-size: 13px; }
  .wait { display: grid; gap: 8px; } .wait .sk { height: 12px; }
</style>
