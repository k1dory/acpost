<script lang="ts">
  import { focusOnMount } from "../lib/actions";
  import { t } from "../lib/i18n.svelte";
  import { app, type Chat } from "../lib/state.svelte";
  import { chatTree } from "../lib/chattree";
  import { ago, screenScale } from "../lib/format";
  import Icon from "./Icon.svelte";
  import AgentMark, { brandOf } from "./AgentMark.svelte";
  import ViewSwitch from "./ViewSwitch.svelte";

  let q = $state("");
  let shown = $state(120);
  let collapsed = $state<Record<string, boolean>>({});
  let naming = $state<string | null>(null); // "new" or a folder id being renamed
  let nameText = $state("");
  // nested: a chat listed under another goes where that one is; it is not moved by itself.
  let menu = $state<{ x: number; y: number; chat?: Chat; folder?: string; nested?: boolean } | null>(null);
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
  // What an agent started from a chat (a subagent's, a teammate's chat) is listed under that chat,
  // wherever its own folder would be; one whose chat is not listed is a chat like any. A filter
  // lists what matches, flat.
  // A branch is where its root is (its folder, or the loose chats), in the place of its most
  // recently active chat.
  const tree = $derived(chatTree(app.chats, !!q));
  const shownBranches = $derived(tree.branches.filter(b => match(b[0].chat)));
  const loose = $derived(shownBranches.filter(b => !b[0].chat.folder));
  const inFolder = (id: string) => shownBranches.filter(b => b[0].chat.folder === id);

  function dotOf(c: Chat) {
    if (c.kind === "cloud") return c.status === "running" ? "running" : "";
    if (c.kind === "session") return ""; // a session started elsewhere: no state known here
    return c.running ? "running" : c.queued ? "queued" : c.failed ? "failed" : ""; // idle: no colour
  }
  // The name field takes the keys as soon as it shows.
  const focusSelect = (el: HTMLInputElement) => focusOnMount(el, true);
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
  function onMenu(e: MouseEvent, chat?: Chat, folder?: string, nested = false) {
    e.preventDefault();
    const k = screenScale(); // the app's zoom taken out: fixed positions are in the page's coordinates
    menu = { x: Math.min(e.clientX / k, innerWidth / k - 230), y: Math.min(e.clientY / k, innerHeight / k - 260), chat, folder, nested };
  }
  // Dragging a chat onto a folder (or back to the list) with the mouse — not the page's own
  // drag and drop, which the app's webview does not carry reliably. Starts after 5 px; the
  // target is what is under the pointer (data-drop); a drag is not also a click.
  let dragging = $state<{ chat: Chat; x: number; y: number } | null>(null);
  let pressed: { chat: Chat; x: number; y: number } | null = null;
  let dragEnd = 0; // when a drag ended: the pointer's click right after it is not a click
  function press(e: PointerEvent, c: Chat) {
    if (e.button !== 0) return;
    pressed = { chat: c, x: e.clientX, y: e.clientY };
    addEventListener("pointermove", move);
    addEventListener("keydown", escape, true);
    addEventListener("pointerup", release, { once: true });
    addEventListener("pointercancel", release, { once: true });
  }
  function targetAt(x: number, y: number): string | null {
    const el = (document.elementFromPoint(x, y) as HTMLElement | null)?.closest("[data-drop]") as HTMLElement | null;
    return el ? el.dataset.drop ?? "" : null;
  }
  function move(e: PointerEvent) {
    if (!pressed) return;
    if (!dragging && Math.hypot(e.clientX - pressed.x, e.clientY - pressed.y) < 5) return;
    const k = screenScale();
    dragging = { chat: pressed.chat, x: e.clientX / k, y: e.clientY / k };
    dropOn = targetAt(e.clientX, e.clientY);
  }
  function stop() {
    removeEventListener("pointermove", move);
    removeEventListener("pointerup", release);
    removeEventListener("pointercancel", release);
    removeEventListener("keydown", escape, true);
    if (dragging) dragEnd = Date.now();
    pressed = null; dragging = null; dropOn = null;
  }
  // Escape drops nothing: the chat stays where it was.
  function escape(e: KeyboardEvent) {
    if (e.key !== "Escape" || !pressed) return;
    e.preventDefault(); e.stopPropagation();
    stop();
  }
  function release(e: PointerEvent) {
    const d = dragging, to = dragging && e.type === "pointerup" ? targetAt(e.clientX, e.clientY) : null;
    stop();
    if (d && to !== null && to !== (d.chat.folder ?? "")) app.moveChat(d.chat.key, to);
  }
  function clickRow(e: MouseEvent, c: Chat) {
    if (e.detail > 0 && Date.now() - dragEnd < 400) return; // the pointer's click ending a drag (keys: detail 0)
    app.open(c);
  }
</script>

<svelte:window onclick={() => (menu = null)} />

<!-- A branch's rows, one after another (not one inside another: a chain is as long as the list);
     past a few levels they stop moving right. -->
{#snippet branch(rows: { chat: Chat; depth: number }[])}
  {#each rows as r (r.chat.key)}{@render row(r.chat, r.depth)}{/each}
{/snippet}

{#snippet row(c: Chat, depth: number)}
  {@const sub = depth > 0}
  {#if renaming === c.key}
    <input class="input fname" bind:value={renameText} use:focusSelect onblur={commitRename}
      onkeydown={e => { if (e.key === "Enter") commitRename(); if (e.key === "Escape") renaming = null; }} />
  {:else if sub}
  <!-- A chat an agent started: who runs it, what it was asked, how it stands. -->
  <button class="row kid" style="margin-left:{Math.min(depth, 5) * 13}px;width:calc(100% - {Math.min(depth, 5) * 13}px)" class:sel={isOpen(c) && app.view === "chat"} onclick={e => clickRow(e, c)} oncontextmenu={e => onMenu(e, c, undefined, true)} title={c.title}>
    <span class="srcic">{#if brandOf(c.cloud_agent || c.agent || "")}<AgentMark of={c.cloud_agent || c.agent || ""} size={12} busy={dotOf(c) === "running"} />{:else}<Icon name={source(c) === "cloud" ? "cloud" : "bot"} size={11} />{/if}</span>
    <span class="t"><span class="title">{c.title || t("Untitled")}</span></span>
    <span class="dot {dotOf(c)}"></span>
  </button>
  {:else}
  <button class="row" class:sel={isOpen(c) && app.view === "chat"} class:lifted={dragging?.chat.key === c.key}
    onpointerdown={e => press(e, c)} onclick={e => clickRow(e, c)} oncontextmenu={e => onMenu(e, c)} title={c.cwd || c.repo || c.title}>
    <span class="dot {dotOf(c)}"></span>
    <span class="t">
      <span class="title">{c.title || t("Untitled")}</span>
      <span class="sub muted"><span class="srcic">{#if brandOf(c.cloud_agent || c.agent || "")}<AgentMark of={c.cloud_agent || c.agent || ""} size={11} busy={dotOf(c) === "running"} />{:else}<Icon name={source(c) === "cloud" ? "cloud" : "bot"} size={10} />{/if}</span>{source(c) === "claude" ? "Claude" : source(c) === "codex" ? "Codex" : source(c) === "cloud" ? c.cloud_agent || "Cloud" : ""}{place(c) ? " · " + place(c) : ""}</span>
    </span>
    <span class="when muted num">{ago(c.updated)}</span>
  </button>
  {/if}
{/snippet}

<aside class="side" class:gone={app.view === "ide"}>
  <div class="top drag"><ViewSwitch /></div>
  <div class="pad">
    <button class="btn primary new nodrag" onclick={() => app.newTask()}><Icon name="plus" /> {t("New task")} <kbd>⌘N</kbd></button>
    <!-- One field: typing filters the chats; ⌘K (or its key here) searches everything. -->
    <label class="search">
      <Icon name="search" />
      <input placeholder={t("Search chats")} bind:value={q} />
      <button class="k" onclick={e => { e.preventDefault(); app.palette = true; }} title={t("Search everything")}>⌘K</button>
    </label>
    <div class="nav">
      <button class="row nr" class:sel={app.view === "board"} onclick={() => (app.view = "board")}>
        <span class="ic"><Icon name="activity" size={14} /></span><span class="t"><span class="title">{t("Tasks")}</span></span>
        {#if app.asking.length}<span class="badge">{app.asking.length}</span>{:else}<kbd>⌘B</kbd>{/if}
      </button>
      <button class="row nr" class:sel={app.view === "wiki"} onclick={() => (app.view = "wiki")}>
        <span class="ic"><Icon name="book" size={14} /></span><span class="t"><span class="title">{t("Wiki")}</span></span>
      </button>
    </div>
  </div>

  <nav class="scroll list">
    <div class="h">
      <span>{t("Folders")}</span>
      <button class="ic-btn" title={t("New folder")} onclick={startNew}><Icon name="plus" size={12} /></button>
    </div>
    {#if naming === "new"}
      <input class="input fname" placeholder={t("Folder name")} bind:value={nameText} onkeydown={keyName} onblur={commitName} use:focusSelect />
    {/if}
    {#each app.folders as f (f.id)}
      {@const items = inFolder(f.id)}
      <div class="folder" class:drop={dropOn === f.id} data-drop={f.id} role="group">
        {#if naming === f.id}
          <input class="input fname" bind:value={nameText} onkeydown={keyName} onblur={commitName} use:focusSelect />
        {:else}
          <button class="fh" onclick={() => toggle(f.id)} ondblclick={() => { naming = f.id; nameText = f.name; }} oncontextmenu={e => onMenu(e, undefined, f.id)}>
            <span class="chev" class:rot={!collapsed[f.id]}><Icon name="chev" size={10} /></span>
            <Icon name="book" size={13} />
            <span class="fname-t">{f.name}</span>
            <span class="muted num cnt">{f.chats.length}</span>
          </button>
        {/if}
        {#if !collapsed[f.id]}
          {#each items as b (b[0].chat.key)}{@render branch(b)}{/each}
          {#if !items.length}<div class="empty-f muted">{t("Drop chats here")}</div>{/if}
        {/if}
      </div>
    {:else}
      {#if naming !== "new"}<div class="none muted">{t("Folders sort your chats: drag one onto a folder.")}</div>{/if}
    {/each}

    <div class="h loose" class:drop={dropOn === ""} data-drop="" role="group">
      <span>{t("Chats")}</span>
      {#if app.loadingChats}<span class="spin"></span>{/if}
    </div>
    {#each loose.slice(0, shown) as b (b[0].chat.key)}{@render branch(b)}{/each}
    {#if loose.length > shown}
      <button class="btn ghost sm more" onclick={() => (shown += 200)}>{t("{n} more", { n: loose.length - shown })}</button>
    {/if}
    {#if !app.chats.length && !app.loadingChats}
      <div class="none muted">{app.conn?.ok ? t("No chats yet") : ""}</div>
    {/if}
    {#each app.partial as p}<div class="partial">{p}</div>{/each}
  </nav>

  <div class="foot">
    <button class="row" class:sel={app.view === "usage"} onclick={() => (app.view = "usage")}><span class="ic"><Icon name="chart" size={14} /></span><span class="t"><span class="title">{t("Usage")}</span></span></button>
    <button class="row" class:sel={app.view === "settings"} onclick={() => (app.view = "settings")}><span class="ic"><Icon name="sun" size={14} /></span><span class="t"><span class="title">{t("Settings")}</span></span><kbd>⌘,</kbd></button>
    <div class="theme">
      <div class="seg">
        <button class:on={app.theme === "light"} onclick={() => app.setTheme("light")} title={t("Light")}><Icon name="sun" size={14} /></button>
        <button class:on={app.theme === "dark"} onclick={() => app.setTheme("dark")} title={t("Dark")}><Icon name="moon" size={14} /></button>
        <button class:on={app.theme === "system"} onclick={() => app.setTheme("system")}>{t("Auto")}</button>
      </div>
      <span class="node muted" title={app.conn?.url}><span class="dot" class:done={app.conn?.ok} class:failed={app.conn && !app.conn.ok}></span>{app.conn?.node ?? "…"}</span>
    </div>
  </div>

  {#if menu}
    <div class="ctx card" style="left:{menu.x}px; top:{menu.y}px">
      {#if menu.chat}
        {@const c = menu.chat}
        <button class="mi" onclick={() => { renaming = c.key; renameText = c.title; }}><Icon name="file" size={12} /> {t("Rename")}</button>
        {#if !menu.nested}
        <div class="mh">{t("Move to")}</div>
        {#each app.folders as f (f.id)}
          <button class="mi" disabled={c.folder === f.id} onclick={() => app.moveChat(c.key, f.id)}><Icon name="book" size={12} /> {f.name}</button>
        {/each}
        <button class="mi" onclick={() => { moveAfter = c.key; startNew(); }}><Icon name="plus" size={12} /> {t("New folder…")}</button>
        {#if c.folder}<button class="mi" onclick={() => app.moveChat(c.key, "")}><Icon name="x" size={12} /> {t("Out of the folder")}</button>{/if}
        {/if}
      {:else if menu.folder}
        {@const id = menu.folder}
        <button class="mi" onclick={() => { naming = id; nameText = app.folders.find(f => f.id === id)?.name ?? ""; }}>{t("Rename")}</button>
        <button class="mi danger" onclick={() => app.deleteFolder(id)}>{t("Delete the folder (its chats stay)")}</button>
      {/if}
    </div>
  {/if}
  {#if dragging}
    <div class="drag-ghost card" style="left:{dragging.x + 12}px;top:{dragging.y + 8}px">{dragging.chat.title || t("Untitled")}</div>
  {/if}
</aside>

<style>
  .side.gone { display: none; }
  .side { width: 260px; flex: none; display: flex; flex-direction: column; background: var(--side); border-right: 1px solid var(--line); min-height: 0; position: relative; }
  .top { height: 50px; flex: none; display: flex; align-items: center; padding-left: 80px; }
  .pad { padding: 4px 14px 12px; display: grid; grid-template-columns: minmax(0, 1fr); gap: 8px; }
  .new { justify-content: center; height: 32px; border-radius: 11px; }
  kbd { font: 11px var(--mono); opacity: .55; margin-left: 4px; }
  .search { display: flex; align-items: center; gap: 8px; height: 30px; padding: 0 10px; border-radius: 11px; font-size: 12.5px; border: 1px solid var(--line); background: var(--card); color: var(--muted); }
  .search input { border: 0; outline: 0; background: transparent; flex: 1; color: var(--ink); min-width: 0; }
  .list { flex: 1; padding: 0 10px 10px; min-height: 0; }
  .h { display: flex; align-items: center; justify-content: space-between; font-size: 10.5px; font-weight: 600; color: var(--muted); padding: 16px 8px 6px; letter-spacing: .02em; text-transform: uppercase; border-radius: 8px; }
  .drag-ghost { position: fixed; z-index: 90; pointer-events: none; padding: 6px 10px; font-size: 12.5px; font-weight: 500; max-width: 220px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; box-shadow: var(--shadow); }
  .row.lifted { opacity: .45; }
  .row.kid { min-height: 28px; padding: 4px 8px; gap: 8px; border-left: 1px solid var(--line); border-radius: 0 var(--r-row) var(--r-row) 0; }
  .folder .row.kid { padding-left: 8px; }
  .row.kid .title { font-size: 12.5px; }
  .h.drop, .folder.drop { background: var(--accent-bg); }
  .search .k { border: 0; background: var(--sel); color: var(--muted); font: 10.5px var(--mono); padding: 2px 6px; border-radius: 6px; cursor: default; }
  .search .k:hover { color: var(--ink); }
  .badge { min-width: 17px; height: 17px; border-radius: 9px; background: var(--warn); color: #fff; font-size: 11px; display: grid; place-items: center; padding: 0 5px; }
  .nav { display: grid; gap: 1px; min-width: 0; }
  .row.nr { min-height: 32px; padding: 5px 8px; }
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
