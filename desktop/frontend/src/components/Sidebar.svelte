<script lang="ts">
  import { app, type Chat } from "../lib/state.svelte";
  import { ago } from "../lib/format";
  import Icon from "./Icon.svelte";

  let q = $state("");
  let shown = $state(120);
  let collapsed = $state<Record<string, boolean>>({});
  let naming = $state<string | null>(null); // "new" or a folder id being renamed
  let nameText = $state("");
  let menu = $state<{ x: number; y: number; chat?: Chat; folder?: string } | null>(null);
  let dropOn = $state<string | null>(null);
  let moveAfter = $state<string | null>(null); // a chat to put in the folder being made
  let renaming = $state<string | null>(null);   // a chat being renamed
  let renameText = $state("");
  async function commitRename() {
    const key = renaming;
    renaming = null;
    if (key) await app.renameChat(key, renameText);
  }

  try { collapsed = JSON.parse(localStorage.getItem("folders-collapsed") ?? "{}"); } catch {}
  function toggle(id: string) {
    collapsed = { ...collapsed, [id]: !collapsed[id] };
    try { localStorage.setItem("folders-collapsed", JSON.stringify(collapsed)); } catch {}
  }

  const match = (c: Chat) => !q || [c.title, c.cwd, c.repo, c.agent].some(s => s?.toLowerCase().includes(q.toLowerCase()));
  const loose = $derived(app.chats.filter(c => !c.folder && match(c)));
  const inFolder = (id: string) => app.chats.filter(c => c.folder === id && match(c));

  function dotOf(c: Chat) {
    if (c.kind === "cloud") return c.status === "running" ? "running" : "";
    if (c.kind === "session") return ""; // a session started elsewhere: no state known here
    return c.running ? "running" : c.queued ? "queued" : c.failed ? "failed" : "done";
  }
  // The name field takes the keys as soon as it shows.
  function focusOnMount(el: HTMLInputElement) { requestAnimationFrame(() => { el.focus(); el.select(); }); }
  function source(c: Chat) {
    if (c.kind === "cloud") return "cloud";
    const a = c.agent ?? "";
    return a.includes("codex") ? "codex" : a.includes("claude") ? "claude" : a.startsWith("agent:sb_") ? "cloud" : "bot";
  }
  function place(c: Chat) {
    const d = c.cwd || c.repo || "";
    if (!d) return "";
    const p = d.replace(/\/+$/, "").split("/");
    return p[p.length - 1] || d;
  }
  const isOpen = (c: Chat) => app.chat?.key === c.key || (!!c.thread && c.thread === app.thread);

  function startNew() { naming = "new"; nameText = ""; }
  async function commitName() {
    const name = nameText.trim();
    const was = naming;
    naming = null;
    if (!name) return;
    if (was === "new") {
      await app.newFolder(name);
      const f = app.folders.find(x => x.name === name);
      if (moveAfter && f) await app.moveChat(moveAfter, f.id);
      moveAfter = null;
    } else if (was) await app.renameFolder(was, name);
  }
  function keyName(e: KeyboardEvent) {
    if (e.key === "Enter") commitName();
    if (e.key === "Escape") naming = null;
  }
  function onMenu(e: MouseEvent, chat?: Chat, folder?: string) {
    e.preventDefault();
    menu = { x: Math.min(e.clientX, innerWidth - 230), y: Math.min(e.clientY, innerHeight - 260), chat, folder };
  }
  function drag(e: DragEvent, c: Chat) { e.dataTransfer?.setData("text/acpost-chat", c.key); }
  function over(e: DragEvent, target: string) {
    if (e.dataTransfer?.types.includes("text/acpost-chat")) { e.preventDefault(); dropOn = target; }
  }
  function drop(e: DragEvent, folder: string) {
    e.preventDefault();
    dropOn = null;
    const key = e.dataTransfer?.getData("text/acpost-chat");
    if (key) app.moveChat(key, folder);
  }
</script>

<svelte:window onclick={() => (menu = null)} />

{#snippet row(c: Chat)}
  {#if renaming === c.key}
    <input class="input fname" bind:value={renameText} use:focusOnMount onblur={commitRename}
      onkeydown={e => { if (e.key === "Enter") commitRename(); if (e.key === "Escape") renaming = null; }} />
  {:else}
  <button class="row" class:sel={isOpen(c) && app.view === "chat"} draggable="true" ondragstart={e => drag(e, c)}
    onclick={() => app.open(c)} oncontextmenu={e => onMenu(e, c)} title={c.cwd || c.repo || c.title}>
    <span class="dot {dotOf(c)}"></span>
    <span class="t">
      <span class="title">{c.title || "Untitled"}</span>
      <span class="sub muted"><span class="srcic"><Icon name={source(c) === "cloud" ? "cloud" : "bot"} size={10} /></span>{source(c) === "claude" ? "Claude" : source(c) === "codex" ? "Codex" : source(c) === "cloud" ? c.cloud_agent || "Cloud" : ""}{place(c) ? " · " + place(c) : ""}</span>
    </span>
    <span class="when muted num">{ago(c.updated)}</span>
  </button>
  {/if}
{/snippet}

<aside class="side">
  <div class="top drag"></div>
  <div class="pad">
    <button class="btn primary new nodrag" onclick={() => app.newTask()}><Icon name="plus" /> New task <kbd>⌘N</kbd></button>
    <div class="quick">
      <button class="row qk" class:sel={app.view === "board"} onclick={() => (app.view = "board")}><span class="ic"><Icon name="activity" size={14} /></span><span class="t"><span class="title">Agents</span></span>{#if app.asking.length}<span class="badge">{app.asking.length}</span>{/if}<kbd>⌘B</kbd></button>
      <button class="row qk" onclick={() => (app.palette = true)}><span class="ic"><Icon name="search" size={14} /></span><span class="t"><span class="title">Search</span></span><kbd>⌘K</kbd></button>
    </div>
    <label class="search">
      <Icon name="search" />
      <input placeholder="Filter chats" bind:value={q} />
    </label>
  </div>

  <nav class="scroll list">
    <div class="h">
      <span>Folders</span>
      <button class="ic-btn" title="New folder" onclick={startNew}><Icon name="plus" size={12} /></button>
    </div>
    {#if naming === "new"}
      <input class="input fname" placeholder="Folder name" bind:value={nameText} onkeydown={keyName} onblur={commitName} use:focusOnMount />
    {/if}
    {#each app.folders as f (f.id)}
      {@const items = inFolder(f.id)}
      <div class="folder" class:drop={dropOn === f.id} ondragover={e => over(e, f.id)} ondragleave={() => (dropOn = null)} ondrop={e => drop(e, f.id)} role="group">
        {#if naming === f.id}
          <input class="input fname" bind:value={nameText} onkeydown={keyName} onblur={commitName} use:focusOnMount />
        {:else}
          <button class="fh" onclick={() => toggle(f.id)} ondblclick={() => { naming = f.id; nameText = f.name; }} oncontextmenu={e => onMenu(e, undefined, f.id)}>
            <span class="chev" class:rot={!collapsed[f.id]}><Icon name="chev" size={10} /></span>
            <Icon name="book" size={13} />
            <span class="fname-t">{f.name}</span>
            <span class="muted num cnt">{f.chats.length}</span>
          </button>
        {/if}
        {#if !collapsed[f.id]}
          {#each items as c (c.key)}{@render row(c)}{/each}
          {#if !items.length}<div class="empty-f muted">Drop chats here</div>{/if}
        {/if}
      </div>
    {:else}
      {#if naming !== "new"}<div class="none muted">Folders sort your chats: drag one onto a folder.</div>{/if}
    {/each}

    <div class="h loose" class:drop={dropOn === ""} ondragover={e => over(e, "")} ondragleave={() => (dropOn = null)} ondrop={e => drop(e, "")} role="group">
      <span>Chats</span>
      {#if app.loadingChats}<span class="spin"></span>{/if}
    </div>
    {#each loose.slice(0, shown) as c (c.key)}{@render row(c)}{/each}
    {#if loose.length > shown}
      <button class="btn ghost sm more" onclick={() => (shown += 200)}>{loose.length - shown} more</button>
    {/if}
    {#if !app.chats.length && !app.loadingChats}
      <div class="none muted">{app.conn?.ok ? "No chats yet" : ""}</div>
    {/if}
    {#each app.partial as p}<div class="partial">{p}</div>{/each}
  </nav>

  <div class="foot">
    <button class="row" class:sel={app.view === "usage"} onclick={() => (app.view = "usage")}><span class="ic"><Icon name="chart" size={14} /></span><span class="t"><span class="title">Usage</span></span></button>
    <button class="row" class:sel={app.view === "settings"} onclick={() => (app.view = "settings")}><span class="ic"><Icon name="sun" size={14} /></span><span class="t"><span class="title">Settings</span></span><kbd>⌘,</kbd></button>
    <div class="theme">
      <div class="seg">
        <button class:on={app.theme === "light"} onclick={() => app.setTheme("light")} title="Light"><Icon name="sun" size={14} /></button>
        <button class:on={app.theme === "dark"} onclick={() => app.setTheme("dark")} title="Dark"><Icon name="moon" size={14} /></button>
        <button class:on={app.theme === "system"} onclick={() => app.setTheme("system")}>Auto</button>
      </div>
      <span class="node muted" title={app.conn?.url}><span class="dot" class:done={app.conn?.ok} class:failed={app.conn && !app.conn.ok}></span>{app.conn?.node ?? "…"}</span>
    </div>
  </div>

  {#if menu}
    <div class="ctx card" style="left:{menu.x}px; top:{menu.y}px">
      {#if menu.chat}
        {@const c = menu.chat}
        <button class="mi" onclick={() => { renaming = c.key; renameText = c.title; }}><Icon name="file" size={12} /> Rename</button>
        <div class="mh">Move to</div>
        {#each app.folders as f (f.id)}
          <button class="mi" disabled={c.folder === f.id} onclick={() => app.moveChat(c.key, f.id)}><Icon name="book" size={12} /> {f.name}</button>
        {/each}
        <button class="mi" onclick={() => { moveAfter = c.key; startNew(); }}><Icon name="plus" size={12} /> New folder…</button>
        {#if c.folder}<button class="mi" onclick={() => app.moveChat(c.key, "")}><Icon name="x" size={12} /> Out of the folder</button>{/if}
      {:else if menu.folder}
        {@const id = menu.folder}
        <button class="mi" onclick={() => { naming = id; nameText = app.folders.find(f => f.id === id)?.name ?? ""; }}>Rename</button>
        <button class="mi danger" onclick={() => app.deleteFolder(id)}>Delete the folder (its chats stay)</button>
      {/if}
    </div>
  {/if}
</aside>

<style>
  .side { width: 260px; flex: none; display: flex; flex-direction: column; background: var(--side); border-right: 1px solid var(--line); min-height: 0; position: relative; }
  .top { height: 38px; flex: none; }
  .pad { padding: 4px 14px 12px; display: grid; gap: 8px; }
  .new { justify-content: center; height: 32px; border-radius: 11px; }
  kbd { font: 11px var(--mono); opacity: .55; margin-left: 4px; }
  .search { display: flex; align-items: center; gap: 8px; height: 30px; padding: 0 10px; border-radius: 11px; font-size: 12.5px; border: 1px solid var(--line); background: var(--card); color: var(--muted); }
  .search input { border: 0; outline: 0; background: transparent; flex: 1; color: var(--ink); min-width: 0; }
  .list { flex: 1; padding: 0 10px 10px; min-height: 0; }
  .h { display: flex; align-items: center; justify-content: space-between; font-size: 10.5px; font-weight: 600; color: var(--muted); padding: 16px 8px 6px; letter-spacing: .02em; text-transform: uppercase; border-radius: 8px; }
  .h.drop, .folder.drop { background: var(--accent-bg); }
  .quick { display: grid; gap: 1px; }
  .qk { min-height: 30px; padding: 4px 8px; }
  .qk kbd { margin-left: auto; }
  .badge { min-width: 17px; height: 17px; border-radius: 9px; background: var(--warn); color: #fff; font-size: 11px; display: grid; place-items: center; padding: 0 5px; }
  .ic-btn { border: 0; background: transparent; color: var(--muted); display: grid; place-items: center; width: 20px; height: 20px; border-radius: 6px; }
  .ic-btn:hover { background: var(--hover); color: var(--ink); }
  .folder { border-radius: 10px; margin-bottom: 2px; }
  .fh { width: 100%; display: flex; align-items: center; gap: 7px; border: 0; background: transparent; padding: 6px 8px; border-radius: 9px; font-weight: 500; text-align: left; }
  .fh:hover { background: var(--hover); }
  .chev { display: grid; color: var(--muted); transition: transform .15s; } .chev.rot { transform: rotate(90deg); }
  .fname-t { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .cnt { font-size: 11px; }
  .fname { height: 28px; margin: 2px 0 6px; font-size: 12.5px; padding: 0 10px; border-radius: 9px; }
  .empty-f { font-size: 11.5px; padding: 4px 8px 8px 34px; }
  .folder .row { padding-left: 18px; }
  .row { width: 100%; display: flex; align-items: center; gap: 10px; text-align: left; border: 0; background: transparent; padding: 6px 8px; border-radius: var(--r-row); min-height: 40px; margin-bottom: 1px; }
  .row:hover { background: var(--hover); }
  .row.sel { background: var(--sel); box-shadow: inset 0 1px 0 var(--edge); }
  .t { flex: 1; min-width: 0; display: grid; }
  .title { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-weight: 500; }
  .sub { font-size: 11px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; display: flex; align-items: center; gap: 4px; }
  .srcic { display: inline-grid; }
  .when { font-size: 11px; }
  .ic { color: var(--muted); display: grid; place-items: center; width: 22px; }
  .none { padding: 4px 8px 8px; font-size: 12px; }
  .partial { font-size: 11px; color: var(--warn); padding: 4px 8px; }
  .more { margin: 4px 8px; }
  .foot { border-top: 1px solid var(--line-soft); padding: 10px; display: grid; gap: 8px; }
  .theme { display: flex; align-items: center; justify-content: space-between; padding: 0 4px 4px; }
  .node { display: inline-flex; gap: 6px; align-items: center; font-size: 12px; max-width: 90px; overflow: hidden; white-space: nowrap; }
  .ctx { position: fixed; z-index: 60; min-width: 220px; padding: 6px; display: grid; }
  .mh { font-size: 10.5px; font-weight: 600; color: var(--muted); text-transform: uppercase; padding: 4px 10px 6px; }
  .mi { display: flex; align-items: center; gap: 8px; border: 0; background: transparent; text-align: left; padding: 7px 10px; border-radius: 8px; font-size: 12.5px; }
  .mi:hover:not(:disabled) { background: var(--hover); }
  .mi:disabled { opacity: .45; }
  .mi.danger { color: var(--crit); }
</style>
