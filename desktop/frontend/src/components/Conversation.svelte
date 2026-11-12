<script lang="ts">
  import { tilde } from "../lib/names";
  import { headerIcon } from "../lib/ui";
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { tick } from "svelte";
  import Logo from "./Logo.svelte";
  import Message from "./Message.svelte";
  import History from "./History.svelte";
  import Composer from "./Composer.svelte";
  import ContextMeter from "./ContextMeter.svelte";
  import Icon from "./Icon.svelte";
  import Dropdown from "./Dropdown.svelte";
  import { errText } from "../lib/api";

  let box = $state<HTMLDivElement>();
  let stick = true;
  const title = $derived(app.chat?.title || (app.msgs[0]?.body ?? "").split("\n")[0].slice(0, 80) || t("New task"));
  const waiting = $derived((app.rev, Object.entries(app.turns).flatMap(([id, t]) => t.waiting().map(r => ({ id, r })))));
  // The context of the latest turn that said it: at the top of the chat.
  const usage = $derived.by(() => {
    app.rev;
    for (let i = app.msgs.length - 1; i >= 0; i--) {
      const u = app.turns[app.msgs[i].id]?.usage;
      if (u) return u;
    }
    for (let i = app.history.length - 1; i >= 0; i--) if (app.history[i].turn.usage) return app.history[i].turn.usage;
    return null;
  });
  const source = $derived(app.chat?.kind === "cloud" ? t("Cloud") : (app.chat?.agent ?? app.target).includes("codex") ? "Codex" : (app.chat?.agent ?? app.target).includes("claude") ? "Claude Code" : "");
  const cwd = $derived(app.chatCwd());
  const short = (d: string) => tilde(d, app.home);

  function onScroll() { if (box) stick = box.scrollHeight - box.scrollTop - box.clientHeight < 80; }
  // Handoff: this chat (on the Mac) goes on in the cloud.
  const canHand = $derived(!!app.cloudAgents.length && !!cwd && !!app.chat && app.chat.kind !== "cloud" && !(app.chat.agent ?? "").startsWith("agent:sb_"));
  let handing = $state(false);
  let toCloud = $state("");
  let note = $state("");
  let handErr = $state("");
  let sending = $state(false);
  // A handoff of this chat still unanswered: the form is as it was (a retry is that handoff).
  function openHand() {
    handing = !handing; handErr = "";
    const was = app.pendingHandoff(app.chat);
    if (was) { toCloud = was.cloud_agent; note = was.note; }
    toCloud = toCloud || app.cloudAgents[0]?.ref || "";
  }
  async function hand() {
    const c = app.chat;
    if (!c || sending) return;
    sending = true; handErr = "";
    try {
      const to = await app.handoff({ thread: c.kind === "thread" ? c.thread ?? "" : "", agent: c.kind === "session" ? c.agent ?? "" : "",
        session: c.kind === "session" ? c.session ?? "" : "", cwd, cloud_agent: toCloud, note });
      handing = false; note = "";
      app.open(to);
    } catch (e) { handErr = errText(e); }
    sending = false;
  }
  $effect(() => { app.rev; app.msgs.length; app.history.length; if (stick) tick().then(() => box && (box.scrollTop = box.scrollHeight)); });
</script>

<svelte:window onclick={e => { if (handing && !(e.target as HTMLElement).closest(".hand, .menu")) handing = false; }} />

<section class="conv">
  <header class="drag" class:beside={app.beside}>
    <div class="t">
      <!-- A new task has its heading in the middle ("What should we work on?"): none up here. -->
      {#if app.chat || app.thread}<div class="title">{title}</div>{/if}
      {#if app.chat || app.thread}
        <div class="sub muted">
          {#if source}<span class="src">{source}</span>{/if}
          {#if cwd}<span class="mono" title={cwd}>{short(cwd)}</span>{/if}
          {#if app.chat?.repo}<span class="mono">{app.chat.repo}</span>{/if}
          {#if app.chat?.status}<span>{app.chat.status}</span>{/if}
        </div>
      {/if}
    </div>
    <span class="sp"></span>
    <!-- Beside the IDE the sidebar is hidden: another chat and a new task are had from here. -->
    {#if app.beside}
      <button class="btn ghost icon nodrag" title={t("New task")} aria-label={t("New task")} onclick={() => app.newTask()}><Icon name="plus" size={headerIcon} /></button>
      <button class="btn ghost icon nodrag" title={t("Another chat (⌘K)")} aria-label={t("Another chat (⌘K)")} onclick={() => (app.palette = true)}><Icon name="chat" size={headerIcon} /></button>
    {/if}
    {#if waiting.length}<span class="chip ask nodrag">{t("{n} to approve", { n: waiting.length })}</span>{/if}
    {#if usage}<div class="meter nodrag"><ContextMeter u={usage} /></div>{/if}
    {#if canHand}
      <div class="hand nodrag">
        <button class="btn ghost icon" class:on={handing} title={t("Continue in the cloud")} aria-label={t("Continue in the cloud")} onclick={openHand}><Icon name="cloud" size={headerIcon} /></button>
        {#if handing}
          <div class="pop card">
            <div class="ph">{t("Continue in the cloud")}</div>
            <div class="muted small">{t("A sandbox starts on this repository's remote and gets the conversation, the base commit and, as a patch, the work not pushed yet. Secrets (.env, keys) stay here.")}</div>
            <Dropdown bind:value={toCloud} icon="cloud" items={app.cloudAgents.map(a => ({ value: a.ref, label: a.name }))} />
            <textarea class="input" rows="3" placeholder={t("What it should do next (optional)")} bind:value={note}></textarea>
            {#if handErr}<div class="err selectable">{handErr}</div>{/if}
            <div class="acts">
              <button class="btn sm" onclick={() => (handing = false)}>{t("Cancel")}</button>
              <button class="btn primary sm" disabled={!toCloud || sending} onclick={hand}>{sending ? t("Handing off…") : t("Hand off")}</button>
            </div>
          </div>
        {/if}
      </div>
    {/if}
    <!-- Beside the IDE there is no right panel to open, and the column is too narrow for more. -->
    {#if !app.beside}
    {#if app.chat?.session && app.chat.kind !== "cloud"}<button class="btn ghost icon nodrag" title={t("Fork: the conversation so far, in a new chat")} aria-label={t("Fork: the conversation so far, in a new chat")} onclick={() => app.forkChat()}><Icon name="fork" size={headerIcon} /></button>{/if}
    <button class="btn ghost icon nodrag" title={t("Right panel (⌘\\)")} aria-label={t("Right panel (⌘\\)")} onclick={() => (app.setPanel(!app.panel))}><Icon name="panel" size={headerIcon} /></button>
    {/if}
  </header>

  {#if !app.chat && !app.thread}
    <!-- A new task: the composer in the middle, with where, who and in which directory. -->
    <div class="center">
      <div class="col">
        <div class="hello">
          <div class="mark"><Logo size={84} draw /></div>
          <div class="big">{t("What should we work on?")}</div>
        </div>
        <Composer />
      </div>
    </div>
  {:else}
    <div class="scroll body" bind:this={box} onscroll={onScroll}>
      <div class="col">
        {#if app.chat && app.chat.kind !== "thread"}<History />{/if}
        {#each app.msgs as m (m.id)}<Message {m} />{/each}
      </div>
    </div>
    <div class="foot"><div class="col"><Composer /></div></div>
  {/if}
</section>

<style>
  .conv { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--page); }
  header { height: 54px; flex: none; display: flex; align-items: center; gap: 12px; padding: 0 16px 0 28px; border-bottom: 1px solid var(--line-soft); }
  .t { min-width: 0; display: grid; gap: 1px; }
  .title { font-weight: 600; font-size: 13.5px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .sub { display: flex; gap: 10px; font-size: 11px; white-space: nowrap; overflow: hidden; }
  .sub .mono { font-size: 11px; overflow: hidden; text-overflow: ellipsis; }
  .src { font-weight: 500; color: var(--ink-2); }
  .sp { flex: 1; }
  .meter { flex: none; }
  .hand { position: relative; }
  .hand .on { background: var(--hover); }
  .pop { position: absolute; right: 0; top: 34px; z-index: 40; width: 340px; padding: 14px; display: grid; gap: 10px; }
  /* Beside the IDE the column is narrow, and what leaves it is under the IDE's own view: the form
     is laid against the header, within the column. */
  header.beside { position: relative; }
  header.beside .hand { position: static; }
  header.beside .pop { right: 12px; top: 46px; width: min(340px, calc(100% - 24px)); }
  .ph { font-weight: 600; font-size: 13px; }
  .pop .small { font-size: 12px; line-height: 1.45; }
  .pop textarea { height: auto; resize: vertical; font-size: 12.5px; }
  .pop .acts { display: flex; gap: 6px; justify-content: flex-end; }
  .pop .err { padding: 8px 10px; border-radius: 10px; background: var(--crit-bg); color: var(--crit); font-size: 12px; white-space: pre-line; }
  .body { flex: 1; min-height: 0; padding: 28px 32px 12px; overflow-x: hidden; }
  .col { max-width: 720px; margin: 0 auto; min-width: 0; }
  .foot { padding: 8px 32px 20px; }
  .center { flex: 1; min-height: 0; display: grid; align-content: center; padding: 0 32px 8vh; }
  .center .col { width: 100%; }
  .hello { padding: 0 0 22px; display: grid; gap: 6px; text-align: center; }
  .mark { display: flex; justify-content: center; margin-bottom: 6px; }
  .big { font-size: 20px; font-weight: 600; letter-spacing: -.01em; }
</style>
