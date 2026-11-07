<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { tick } from "svelte";
  import Message from "./Message.svelte";
  import History from "./History.svelte";
  import Composer from "./Composer.svelte";
  import ContextMeter from "./ContextMeter.svelte";
  import Icon from "./Icon.svelte";

  let box = $state<HTMLDivElement>();
  let stick = true;
  const title = $derived(app.chat?.title || (app.msgs[0]?.body ?? "").split("\n")[0].slice(0, 80) || "New task");
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
  const source = $derived(app.chat?.kind === "cloud" ? "Cloud" : (app.chat?.agent ?? app.target).includes("codex") ? "Codex" : (app.chat?.agent ?? app.target).includes("claude") ? "Claude Code" : "");
  const cwd = $derived(app.chatCwd());
  const short = (d: string) => (app.home && d.startsWith(app.home) ? "~" + d.slice(app.home.length) : d);

  function onScroll() { if (box) stick = box.scrollHeight - box.scrollTop - box.clientHeight < 80; }
  $effect(() => { app.rev; app.msgs.length; app.history.length; if (stick) tick().then(() => box && (box.scrollTop = box.scrollHeight)); });
</script>

<section class="conv">
  <header class="drag">
    <div class="t">
      <div class="title">{title}</div>
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
    {#if waiting.length}<span class="chip ask nodrag">{waiting.length} needs approval</span>{/if}
    {#if usage}<div class="meter nodrag"><ContextMeter u={usage} /></div>{/if}
    {#if app.chat?.session && app.chat.kind !== "cloud"}<button class="btn ghost sm nodrag" title="Fork: the conversation so far, in a new chat" onclick={() => app.forkChat()}><Icon name="fork" size={14} /></button>{/if}
    <button class="btn ghost sm nodrag" title="Right panel (⌘\)" onclick={() => (app.panel = !app.panel)}><Icon name="panel" size={14} /></button>
  </header>

  {#if !app.chat && !app.thread}
    <!-- A new task: the composer in the middle, with where, who and in which directory. -->
    <div class="center">
      <div class="col">
        <div class="hello">
          <div class="big">What should we work on?</div>
          <div class="muted">Where it runs, the agent and its directory — then the task. Type / for the model and effort.</div>
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
  .body { flex: 1; min-height: 0; padding: 28px 32px 12px; overflow-x: hidden; }
  .col { max-width: 720px; margin: 0 auto; min-width: 0; }
  .foot { padding: 8px 32px 20px; }
  .center { flex: 1; min-height: 0; display: grid; align-content: center; padding: 0 32px 8vh; }
  .center .col { width: 100%; }
  .hello { padding: 0 0 22px; display: grid; gap: 6px; text-align: center; }
  .big { font-size: 20px; font-weight: 600; letter-spacing: -.01em; }
</style>
