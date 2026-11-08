<script lang="ts">
  import { t, plural } from "../lib/i18n.svelte";
  import { app, type Chat } from "../lib/state.svelte";
  import { ago, who } from "../lib/format";
  import Icon from "./Icon.svelte";
  import Dropdown from "./Dropdown.svelte";
  import { Node, errText } from "../lib/api";

  // The backlog: tasks written down for later; started, one goes to its agent and leaves.
  type Item = { id: string; title: string; body?: string; agent?: string; cwd?: string; created: string };
  let backlog = $state<Item[]>([]);
  let adding = $state(false);
  let draft = $state<{ id?: string; title: string; body: string; agent: string; cwd: string }>({ title: "", body: "", agent: "", cwd: "" });
  let busy = $state(false);
  async function loadBacklog() {
    try { backlog = ((await Node.Backlog()) ?? []) as any; } catch (e) { app.error = errText(e); }
  }
  $effect(() => { loadBacklog(); });
  function startAdd(it?: Item) {
    draft = it ? { id: it.id, title: it.title, body: it.body ?? "", agent: it.agent ?? "", cwd: it.cwd ?? "" }
      : { title: "", body: "", agent: app.localAgents()[0]?.address ?? "", cwd: app.draft.cwd || app.home };
    adding = true;
  }
  async function saveItem() {
    if (!draft.title.trim() || busy) return;
    busy = true;
    try { await Node.PutBacklogItem(draft as any); adding = false; await loadBacklog(); } catch (e) { app.error = errText(e); }
    busy = false;
  }
  async function pickDir() {
    const d = await Node.PickDirectory(draft.cwd || app.home).catch(() => "");
    if (d) draft.cwd = d;
  }
  async function start(it: Item) {
    try {
      const thread = await Node.StartBacklogItem(it.id, "");
      await Promise.all([loadBacklog(), app.loadChats()]);
      app.openKey("thread:" + thread);
    } catch (e) { app.error = errText(e); }
  }
  async function remove(it: Item) {
    try { await Node.DeleteBacklogItem(it.id); await loadBacklog(); } catch (e) { app.error = errText(e); }
  }
  const agentLabel = (a?: string) => { const n = (a ?? "").replace(/^agent:/, ""); return n === "claude" ? "Claude Code" : n === "codex" ? "Codex" : n; };
  const tilde = (d?: string) => (!d ? "" : app.home && d.startsWith(app.home) ? "~" + d.slice(app.home.length) : d);

  // Every agent's work at once: what waits for you, what runs, what failed, what is done.
  const asksByThread = $derived.by(() => {
    const m = new Map<string, number>();
    for (const r of app.asking) {
      const key = "thread:" + (r.thread || r.message);
      m.set(key, (m.get(key) ?? 0) + 1);
    }
    return m;
  });
  const threads = $derived(app.chats.filter(c => c.kind === "thread" || c.kind === "cloud"));
  const week = Date.now() - 7 * 86400e3;
  const cols = $derived([
    { key: "wait", title: "Needs you", items: threads.filter(c => asksByThread.has(c.key)) },
    { key: "run", title: "Running", items: threads.filter(c => !asksByThread.has(c.key) && ((c.running ?? 0) + (c.queued ?? 0) > 0 || c.status === "running")) },
    { key: "fail", title: "Failed", items: threads.filter(c => !asksByThread.has(c.key) && !c.running && !c.queued && (c.failed ?? 0) > 0) },
    { key: "done", title: "Done", items: threads.filter(c => !asksByThread.has(c.key) && !c.running && !c.queued && !c.failed && c.status !== "running" && Date.parse(c.updated) > week).slice(0, 40) },
  ]);
  function agentName(c: Chat) {
    const a = c.agent ?? "";
    return a.includes("codex") ? "Codex" : a.includes("claude") ? "Claude Code" : c.kind === "cloud" ? c.cloud_agent || "Cloud" : who(a).name;
  }
  function place(c: Chat) {
    const d = c.cwd || c.repo || "";
    return d.split("/").filter(Boolean).pop() ?? "";
  }
</script>

<section class="board">
  <header class="drag">
    <h1>{t("Tasks")}</h1>
    <span class="muted">{plural(threads.length, "task", "tasks")}</span>
    <span class="sp"></span>
    <button class="btn primary sm nodrag" onclick={() => app.newTask()}><Icon name="plus" size={12} /> {t("New task")}</button>
  </header>
  <div class="cols">
    <div class="col backlog">
      <div class="ch"><span class="dot"></span>{t("Backlog")}<span class="muted num">{backlog.length}</span>
        <button class="add" onclick={() => startAdd()} title={t("Add to the backlog")} aria-label={t("Add to the backlog")}><Icon name="plus" size={12} /></button></div>
      <div class="cards scroll">
        {#if adding}
          <div class="card item form">
            <!-- svelte-ignore a11y_autofocus -->
            <input class="input" placeholder={t("What to do")} bind:value={draft.title} autofocus onkeydown={e => { if (e.key === "Enter") saveItem(); if (e.key === "Escape") adding = false; }} />
            <textarea class="input" rows="3" placeholder={t("Details (optional)")} bind:value={draft.body}></textarea>
            <div class="frow">
              <Dropdown bind:value={draft.agent} icon="bot" items={app.localAgents().map(a => ({ value: a.address, label: agentLabel(a.address) }))} />
              <button class="chip pick" onclick={pickDir} title={draft.cwd}><Icon name="file" size={11} /> {tilde(draft.cwd) || "~"}</button>
            </div>
            <div class="acts">
              <button class="btn sm" onclick={() => (adding = false)}>{t("Cancel")}</button>
              <button class="btn primary sm" disabled={!draft.title.trim() || busy} onclick={saveItem}>{t("Save")}</button>
            </div>
          </div>
        {/if}
        {#each backlog as it (it.id)}
          <div class="card item bl">
            <button class="tt plain" onclick={() => startAdd(it)} title={t("Edit")}>{it.title}</button>
            {#if it.body}<div class="bd muted">{it.body}</div>{/if}
            <div class="meta muted">
              {#if it.agent}<span><Icon name="bot" size={11} /> {agentLabel(it.agent)}</span>{/if}
              {#if it.cwd}<span class="mono">{tilde(it.cwd).split("/").filter(Boolean).pop()}</span>{/if}
              <span class="sp"></span>
              <button class="icon-btn" onclick={() => remove(it)} title={t("Delete")} aria-label={t("Delete")}><Icon name="x" size={11} /></button>
              <button class="btn primary sm go" onclick={() => start(it)}><Icon name="send" size={11} /> {t("Start")}</button>
            </div>
          </div>
        {:else}
          {#if !adding}<button class="none muted plain" onclick={() => startAdd()}>{t("Write down a task for later")}</button>{/if}
        {/each}
      </div>
    </div>
    {#each cols as col (col.key)}
      <div class="col">
        <div class="ch"><span class="dot {col.key === 'run' ? 'running' : col.key === 'fail' ? 'failed' : col.key === 'wait' ? 'queued' : 'done'}"></span>{t(col.title)}<span class="muted num">{col.items.length}</span></div>
        <div class="cards scroll">
          {#each col.items as c (c.key)}
            <button class="card item" onclick={() => app.open(c)}>
              <div class="tt">{c.title || t("Untitled")}</div>
              <div class="meta muted">
                <span><Icon name={c.kind === "cloud" ? "cloud" : "bot"} size={11} /> {agentName(c)}</span>
                {#if place(c)}<span class="mono">{place(c)}</span>{/if}
                <span class="sp"></span>
                {#if col.key === "wait"}<span class="chip ask">{t("{n} to approve", { n: asksByThread.get(c.key) ?? 0 })}</span>{/if}
                {#if col.key === "run"}<span class="spin"></span>{/if}
                <span class="num">{ago(c.updated)}</span>
              </div>
            </button>
          {:else}
            <div class="none muted">—</div>
          {/each}
        </div>
      </div>
    {/each}
  </div>
</section>

<style>
  .board { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--page); }
  header { height: 54px; flex: none; display: flex; align-items: center; gap: 12px; padding: 0 20px 0 28px; border-bottom: 1px solid var(--line-soft); }
  h1 { font-size: 15px; margin: 0; }
  .sp { flex: 1; }
  .cols { flex: 1; min-height: 0; display: grid; grid-template-columns: repeat(5, minmax(220px, 1fr)); gap: 14px; padding: 16px 20px; overflow-x: auto; }
  .col { display: flex; flex-direction: column; min-height: 0; background: var(--side); border: 1px solid var(--line-soft); border-radius: var(--r-panel); padding: 10px; }
  .ch { display: flex; align-items: center; gap: 8px; font-weight: 600; font-size: 12.5px; padding: 2px 6px 10px; }
  .ch .num { margin-left: auto; font-weight: 400; }
  .cards { flex: 1; min-height: 0; display: grid; align-content: start; gap: 8px; }
  .item { text-align: left; padding: 10px 12px; display: grid; gap: 8px; border-radius: 16px; }
  .item:hover { background: linear-gradient(180deg, var(--sheen), transparent), var(--hover); }
  .tt { font-weight: 500; font-size: 12.5px; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; overflow: hidden; }
  .meta { display: flex; align-items: center; gap: 8px; font-size: 11px; }
  .meta span { display: inline-flex; align-items: center; gap: 4px; }
  .none { text-align: center; padding: 16px; font-size: 12px; }
  .plain { border: 0; background: transparent; text-align: left; padding: 0; color: inherit; font: inherit; cursor: default; }
  .none.plain { text-align: center; padding: 16px; width: 100%; }
  .add { margin-left: 6px; width: 22px; height: 22px; border: 0; background: transparent; border-radius: 6px; color: var(--muted); display: grid; place-items: center; }
  .add:hover { background: var(--hover); color: var(--ink); }
  .bl { cursor: default; }
  .bd { font-size: 11.5px; display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; white-space: pre-line; }
  .go { height: 22px; padding: 0 8px; font-size: 11px; gap: 4px; display: inline-flex; align-items: center; }
  .icon-btn { border: 0; background: transparent; color: var(--muted); width: 20px; height: 20px; border-radius: 5px; display: grid; place-items: center; }
  .icon-btn:hover { background: var(--hover); color: var(--crit); }
  .form { gap: 8px; }
  .form .input { height: 30px; font-size: 12.5px; }
  .form textarea.input { height: auto; resize: vertical; }
  .frow { display: flex; gap: 6px; flex-wrap: wrap; align-items: center; }
  .pick { height: 26px; cursor: default; font-size: 11.5px; max-width: 160px; overflow: hidden; }
  .acts { display: flex; gap: 6px; justify-content: flex-end; }
</style>
