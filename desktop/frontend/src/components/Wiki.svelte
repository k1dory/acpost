<script lang="ts">
  import { t, i18n } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node, errText } from "../lib/api";
  import { ago } from "../lib/format";
  import Icon from "./Icon.svelte";
  import Dropdown from "./Dropdown.svelte";
  import Markdown from "./Markdown.svelte";

  // The wikis of repositories: an agent of this Mac reads a directory and writes its pages.
  type Wiki = { repo: string; agent: string; lang?: string; status: string; msg?: string; error?: string; updated: string; pages: number };
  type Page = { slug: string; title: string; parent?: string; body?: string };

  let wikis = $state<Wiki[] | null>(null);
  let repo = $state("");
  let pages = $state<Page[]>([]);
  let page = $state("");
  let err = $state("");
  let adding = $state(false);
  let newDir = $state("");
  let newAgent = $state("");

  const cur = $derived(wikis?.find(w => w.repo === repo));
  const shown = $derived(pages.find(p => p.slug === page) ?? pages[0]);
  const tops = $derived(pages.filter(p => !p.parent));
  const kids = (slug: string) => pages.filter(p => p.parent === slug);
  const base = (d: string) => d.split("/").filter(Boolean).pop() || d;
  const tilde = (d: string) => (app.home && d.startsWith(app.home) ? "~" + d.slice(app.home.length) : d);
  const agentName = (a: string) => { const n = a.replace(/^agent:/, ""); return n === "claude" ? "Claude Code" : n === "codex" ? "Codex" : n; };

  async function load() {
    try { wikis = ((await Node.Wikis()) ?? []) as any; } catch (e) { err = errText(e); wikis = []; }
    if (!repo && wikis?.length) pick(wikis[0].repo);
    else if (repo && cur?.status === "ready" && !pages.length) loadPages();
  }
  async function loadPages() {
    try { pages = ((await Node.WikiPages(repo)) ?? []) as any; } catch (e) { err = errText(e); pages = []; }
  }
  function pick(r: string) { repo = r; page = ""; pages = []; err = ""; adding = false; loadPages(); }
  $effect(() => { load(); });
  // While one is being written, look again now and then; when it is done, its pages come.
  $effect(() => {
    if (!wikis?.some(w => w.status === "writing")) return;
    const id = setInterval(async () => {
      const before = cur?.status;
      await load();
      if (before === "writing" && cur?.status === "ready") loadPages();
    }, 5000);
    return () => clearInterval(id);
  });

  async function write(dir: string, agent: string) {
    err = "";
    try {
      await Node.WriteWiki(dir, agent, i18n.lang);
      adding = false;
      repo = dir;
      await load();
    } catch (e) { err = errText(e); }
  }
  async function remove() {
    if (!cur) return;
    try { await Node.DeleteWiki(cur.repo); repo = ""; pages = []; await load(); } catch (e) { err = errText(e); }
  }
  async function chooseDir() {
    const d = await Node.PickDirectory(newDir || app.home).catch(() => "");
    if (d) newDir = d;
  }
  async function openTask() {
    if (!cur?.msg) return;
    await app.loadChats();
    app.view = "chat";
    app.openKey("thread:" + cur.msg);
  }
  // A question about the repository: a new task in its directory, the wiki's page as context.
  function ask() {
    if (!cur) return;
    app.newTask();
    app.draft.where = "mac";
    app.draft.cwd = cur.repo;
    if (cur.agent) app.draft.agent = cur.agent;
    if (shown) app.quote = t("About “{title}” in the wiki of {repo}:", { title: shown.title, repo: base(cur.repo) }) + "\n\n";
    app.view = "chat";
  }
  function startNew() {
    adding = true;
    newDir = app.draft.cwd || app.home;
    newAgent = app.localAgents()[0]?.address ?? "";
  }
</script>

<section class="wk">
  <header class="drag">
    <h1>{t("Wiki")}</h1>
    <span class="sp"></span>
    <button class="btn primary sm nodrag" onclick={startNew}><Icon name="plus" size={12} /> {t("New wiki")}</button>
  </header>
  <div class="cols">
    <nav class="repos">
      {#each wikis ?? [] as w (w.repo)}
        <button class="rp" class:on={w.repo === repo && !adding} onclick={() => pick(w.repo)} title={w.repo}>
          <span class="nm">{base(w.repo)}</span>
          <span class="muted small">
            {#if w.status === "writing"}<span class="spin"></span> {t("writing…")}{:else if w.status === "failed"}<span class="bad">{t("failed")}</span>{:else}{w.pages} · {ago(w.updated)}{/if}
          </span>
        </button>
      {:else}
        <div class="none muted">{wikis === null ? t("Loading…") : t("No wikis yet")}</div>
      {/each}
    </nav>

    {#if adding}
      <div class="main">
        <div class="form card">
          <h2>{t("New wiki")}</h2>
          <p class="muted">{t("An agent of this Mac reads the repository — without changing anything — and writes its wiki: an overview, the architecture with diagrams, the modules, how to build and run it.")}</p>
          <div class="lbl">{t("Repository")}</div>
          <button class="chip pickd" onclick={chooseDir}><Icon name="file" size={12} /> {tilde(newDir) || t("Choose…")}</button>
          <div class="lbl">{t("Agent")}</div>
          <Dropdown bind:value={newAgent} icon="bot" items={app.localAgents().map(a => ({ value: a.address, label: agentName(a.address) }))} />
          {#if err}<div class="err selectable">{err}</div>{/if}
          <div class="acts">
            <button class="btn" onclick={() => (adding = false)}>{t("Cancel")}</button>
            <button class="btn primary" disabled={!newDir || !newAgent} onclick={() => write(newDir, newAgent)}>{t("Write the wiki")}</button>
          </div>
        </div>
      </div>
    {:else if !cur}
      <div class="main"><div class="empty">
        <Icon name="book" size={28} />
        <div class="big">{t("A wiki for each of your repositories")}</div>
        <div class="muted">{t("An agent reads the code and writes the pages; ask about the repository from any page.")}</div>
        <button class="btn primary" onclick={startNew}><Icon name="plus" size={12} /> {t("New wiki")}</button>
      </div></div>
    {:else}
      <nav class="tree">
        <div class="rhead" title={cur.repo}><b>{base(cur.repo)}</b><span class="muted small mono">{tilde(cur.repo)}</span></div>
        {#each tops as p (p.slug)}
          <button class="pg" class:on={shown?.slug === p.slug} onclick={() => (page = p.slug)}>{p.title}</button>
          {#each kids(p.slug) as c (c.slug)}
            <button class="pg sub" class:on={shown?.slug === c.slug} onclick={() => (page = c.slug)}>{c.title}</button>
          {/each}
        {/each}
      </nav>
      <div class="main">
        <div class="bar">
          <span class="muted small">{agentName(cur.agent)} · {ago(cur.updated)}</span>
          <span class="sp"></span>
          {#if cur.msg}<button class="btn ghost sm" onclick={openTask}>{t("The task")}</button>{/if}
          <button class="btn ghost sm" disabled={cur.status === "writing"} onclick={() => write(cur!.repo, cur!.agent)}>{t("Write again")}</button>
          <button class="btn ghost sm danger" onclick={remove}>{t("Delete")}</button>
          <button class="btn primary sm" onclick={ask}>{t("Ask about it")}</button>
        </div>
        {#if err}<div class="err selectable">{err}</div>{/if}
        {#if cur.status === "writing" && !pages.length}
          <div class="empty"><span class="spin"></span><div>{t("The agent is reading the repository and writing the wiki. It takes a few minutes; the task shows what it does.")}</div></div>
        {:else if cur.status === "failed" && !pages.length}
          <div class="err selectable">{t("The wiki was not written")}: {cur.error}</div>
        {:else if shown}
          <article class="page">
            {#if cur.status === "writing"}<div class="note muted"><span class="spin"></span> {t("Being written again; these are the pages before.")}</div>{/if}
            <h1 class="pt">{shown.title}</h1>
            <Markdown text={shown.body ?? ""} />
          </article>
        {/if}
      </div>
    {/if}
  </div>
</section>

<style>
  .wk { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--page); }
  header { height: 54px; display: flex; align-items: center; gap: 10px; padding: 0 20px 0 28px; border-bottom: 1px solid var(--line-soft); flex: none; }
  h1 { font-size: 15px; margin: 0; }
  h2 { font-size: 15px; margin: 0; }
  .sp { flex: 1; }
  .cols { flex: 1; min-height: 0; display: flex; }
  .repos, .tree { width: 200px; flex: none; padding: 12px 8px; display: flex; flex-direction: column; gap: 2px; border-right: 1px solid var(--line-soft); overflow: auto; }
  .tree { width: 220px; }
  .rp, .pg { display: grid; gap: 1px; border: 0; background: transparent; text-align: left; padding: 7px 10px; border-radius: 10px; color: var(--ink-2); }
  .rp:hover, .pg:hover { background: var(--hover); }
  .rp.on, .pg.on { background: var(--sel); color: var(--ink); }
  .nm { font-weight: 500; font-size: 13px; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .small { font-size: 11.5px; display: inline-flex; align-items: center; gap: 5px; }
  .pg { font-size: 12.5px; }
  .pg.sub { padding-left: 24px; }
  .rhead { display: grid; gap: 2px; padding: 4px 10px 10px; border-bottom: 1px solid var(--line-soft); margin-bottom: 6px; }
  .rhead .mono { font-size: 10.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .main { flex: 1; min-width: 0; overflow: auto; display: flex; flex-direction: column; }
  .bar { display: flex; align-items: center; gap: 6px; padding: 10px 20px; border-bottom: 1px solid var(--line-soft); position: sticky; top: 0; background: var(--page); z-index: 2; }
  .page { max-width: 820px; width: 100%; margin: 0 auto; padding: 22px 32px 60px; }
  .pt { font-size: 21px; margin: 0 0 14px; letter-spacing: -.01em; }
  .note { font-size: 12px; display: flex; align-items: center; gap: 6px; margin-bottom: 10px; }
  .empty { margin: auto; display: grid; justify-items: center; gap: 10px; text-align: center; max-width: 420px; padding: 40px; color: var(--ink-2); }
  .big { font-size: 16px; font-weight: 600; color: var(--ink); }
  .none { font-size: 12px; padding: 8px 10px; }
  .form { margin: 28px auto; width: min(560px, calc(100% - 48px)); padding: 20px; display: grid; gap: 10px; justify-items: start; }
  .form p { margin: 0; font-size: 12.5px; }
  .lbl { font-size: 11px; font-weight: 600; color: var(--muted); text-transform: uppercase; margin-top: 4px; }
  .pickd { height: 28px; cursor: default; }
  .acts { display: flex; gap: 8px; justify-self: end; margin-top: 6px; }
  .err { padding: 10px 12px; margin: 12px 20px; border-radius: 12px; background: var(--crit-bg); color: var(--crit); font-size: 12.5px; white-space: pre-line; }
  .form .err { margin: 0; justify-self: stretch; }
  .bad { color: var(--crit); }
</style>
