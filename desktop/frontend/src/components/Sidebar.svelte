<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { ago, who } from "../lib/format";
  import Icon from "./Icon.svelte";

  let q = $state("");
  const convs = $derived(app.convs.filter(c => !q || c.title.toLowerCase().includes(q.toLowerCase()) || c.agents.some(a => a.includes(q))));
  const groups = $derived({
    mine: app.agents.filter(a => a.driver === "acp" || a.driver === "inbox"),
    cloud: app.agents.filter(a => a.driver === "sandbox"),
    remote: app.agents.filter(a => a.driver === "remote"),
  });
  function dotOf(c: (typeof app.convs)[number]) {
    return c.running ? "running" : c.queued ? "queued" : c.failed ? "failed" : "done";
  }
  function startWith(addr: string) {
    app.target = addr;
    app.open("");
  }
</script>

<aside class="side">
  <div class="top drag"></div>
  <div class="pad">
    <button class="btn primary new nodrag" onclick={() => app.open("")}><Icon name="plus" /> New task <kbd>⌘N</kbd></button>
    <label class="search">
      <Icon name="search" />
      <input placeholder="Search conversations" bind:value={q} />
    </label>
  </div>

  <nav class="scroll list">
    <div class="h">Conversations</div>
    {#each convs as c (c.thread)}
      <button class="row" class:sel={c.thread === app.thread && app.view === "chat"} onclick={() => app.open(c.thread)}>
        <span class="dot {dotOf(c)}"></span>
        <span class="t">
          <span class="title">{c.title || "Untitled"}</span>
          <span class="sub muted">{c.agents.map(a => who(a).name).join(", ")}{c.messages > 1 ? ` · ${c.messages}` : ""}</span>
        </span>
        <span class="when muted num">{ago(c.updated)}</span>
      </button>
    {:else}
      {#if app.conn?.ok}
        <div class="none muted">No conversations yet</div>
      {:else}
        {#each [0, 1, 2, 3] as _}<div class="sk skrow"></div>{/each}
      {/if}
    {/each}
    {#if app.convNext}
      <button class="btn ghost sm more" onclick={() => app.loadConvs(true)}>More</button>
    {/if}

    {#each [["Agents", groups.mine, "bot"], ["Cloud", groups.cloud, "cloud"], ["Other nodes", groups.remote, "bot"]] as [label, list, icon]}
      {#if (list as any[]).length}
        <div class="h">{label}</div>
        {#each list as any[] as a (a.address)}
          <button class="row agent" onclick={() => startWith(a.address)} title={a.address}>
            <span class="ic"><Icon name={icon as string} size={14} /></span>
            <span class="t"><span class="title">{a.name}</span>{#if a.node}<span class="sub muted">{a.node}</span>{/if}</span>
            {#if a.running}<span class="chip">running {a.running}</span>{:else if a.queued}<span class="chip warn">{a.queued} queued</span>{/if}
          </button>
        {/each}
      {/if}
    {/each}
  </nav>

  <div class="foot">
    <button class="row" class:sel={app.view === "usage"} onclick={() => (app.view = "usage")}><span class="ic"><Icon name="chart" size={14} /></span><span class="t"><span class="title">Usage</span></span></button>
    <div class="theme">
      <div class="seg">
        <button class:on={app.theme === "light"} onclick={() => app.setTheme("light")} title="Light"><Icon name="sun" size={14} /></button>
        <button class:on={app.theme === "dark"} onclick={() => app.setTheme("dark")} title="Dark"><Icon name="moon" size={14} /></button>
        <button class:on={app.theme === "system"} onclick={() => app.setTheme("system")}>Auto</button>
      </div>
      <span class="node muted" title={app.conn?.url}><span class="dot" class:done={app.conn?.ok} class:failed={app.conn && !app.conn.ok}></span>{app.conn?.node ?? "…"}</span>
    </div>
  </div>
</aside>

<style>
  .side { width: 264px; flex: none; display: flex; flex-direction: column; background: var(--side); border-right: 1px solid var(--line); min-height: 0; }
  .top { height: 40px; flex: none; }
  .pad { padding: 4px 12px 10px; display: grid; gap: 8px; }
  .new { justify-content: center; height: 36px; border-radius: 14px; }
  kbd { font: 11px var(--mono); opacity: .55; margin-left: 4px; }
  .search { display: flex; align-items: center; gap: 8px; height: 34px; padding: 0 11px; border-radius: 14px; border: 1px solid var(--line); background: var(--card); color: var(--muted); }
  .search input { border: 0; outline: 0; background: transparent; flex: 1; color: var(--ink); min-width: 0; }
  .list { flex: 1; padding: 0 8px 8px; min-height: 0; }
  .h { font-size: 11.5px; font-weight: 600; color: var(--muted); padding: 14px 8px 6px; letter-spacing: .02em; text-transform: uppercase; }
  .row { width: 100%; display: flex; align-items: center; gap: 10px; text-align: left; border: 0; background: transparent; padding: 8px; border-radius: var(--r-row); min-height: 44px; }
  .row:hover { background: var(--hover); }
  .row.sel { background: var(--sel); box-shadow: inset 0 1px 0 var(--edge); }
  .row.agent { min-height: 36px; padding: 6px 8px; }
  .t { flex: 1; min-width: 0; display: grid; }
  .title { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-weight: 500; }
  .sub { font-size: 12px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .when { font-size: 11.5px; }
  .ic { color: var(--muted); display: grid; place-items: center; width: 22px; }
  .none { padding: 8px; font-size: 13px; }
  .skrow { height: 36px; margin: 6px 8px; }
  .more { margin: 4px 8px; }
  .foot { border-top: 1px solid var(--line-soft); padding: 8px; display: grid; gap: 6px; }
  .theme { display: flex; align-items: center; justify-content: space-between; padding: 0 4px 4px; }
  .node { display: inline-flex; gap: 6px; align-items: center; font-size: 12px; max-width: 90px; overflow: hidden; white-space: nowrap; }
</style>
