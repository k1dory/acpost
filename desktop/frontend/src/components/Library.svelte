<script lang="ts">
  import { Browser } from "@wailsio/runtime";
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node, errText } from "../lib/api";
  import { inView } from "../lib/visible";
  import { itemKey, refOf, webLink, type LibItem } from "../lib/library";
  import Icon from "./Icon.svelte";
  import LibraryCard from "./LibraryCard.svelte";
  import ArtifactView from "./ArtifactView.svelte";

  // The library: the artifacts of every chat, newest first — what agents attached to their replies,
  // and the documents their turns made on this Mac — a page at a time, filtered by name or address.
  const pageSize = 60;
  let items = $state<LibItem[] | null>(null);
  let next = $state("");
  let q = $state("");
  let err = $state("");
  let more = $state(false);
  let opened = $state<LibItem | null>(null);
  let gen = 0;
  // Bumped by Refresh: the cards ask for their previews again.
  let stamp = $state(0);
  // A first page is being asked for: no page after the old one's cursor meanwhile.
  let reloading = $state(false);

  // A first page (again): what an older filter asked for, answered late, is dropped. The old
  // cursor goes at once: a page after it would land right after the new first page.
  async function load() {
    const my = ++gen;
    err = ""; next = ""; more = false; reloading = true;
    try {
      const r = (await Node.Artifacts(q.trim(), "", pageSize)) as any;
      if (my !== gen) return;
      items = r?.items ?? [];
      next = r?.next ?? "";
    } catch (e) { if (my === gen) { err = errText(e); items ??= []; } }
    finally { if (my === gen) reloading = false; }
  }
  function refresh() { stamp++; load(); }
  // The page after: asked once the end of the list comes near, never while a first page is.
  async function loadMore() {
    if (!next || more || reloading) return;
    const my = gen, from = next;
    more = true;
    try {
      const r = (await Node.Artifacts(q.trim(), from, pageSize)) as any;
      if (my !== gen) return;
      const seen = new Set((items ?? []).map(itemKey));
      items = [...(items ?? []), ...((r?.items ?? []) as LibItem[]).filter(a => !seen.has(itemKey(a)))];
      next = r?.next ?? "";
    } catch (e) { if (my === gen) err = errText(e); }
    finally { if (my === gen) more = false; }
  }
  // Typing filters once it pauses.
  let first = true;
  $effect(() => {
    q;
    if (first) { first = false; load(); return; }
    const id = setTimeout(load, 250);
    return () => clearTimeout(id);
  });

  function open(a: LibItem) {
    const web = webLink(a);
    if (web) { Browser.OpenURL(web); return; }
    opened = a;
  }
  // Its chat: the one listed, or (older than the chats listed) the thread itself. Only the latest
  // click opens one, and none once the library is gone: an answer that comes late changes nothing.
  let opening = 0;
  $effect(() => () => { opening++; });
  async function openChat(a: LibItem) {
    if (!a.thread) return;
    const my = ++opening;
    await app.loadChats();
    if (my !== opening) return;
    const key = "thread:" + a.thread;
    app.view = "chat";
    const listed = app.chats.find(c => c.key === key);
    app.open(listed ?? { key, kind: "thread", thread: a.thread, title: a.title ?? "", agent: a.agent, cwd: a.cwd, updated: a.at });
  }
</script>

<section class="lib">
  <header class="drag">
    {#if opened}
      <button class="btn ghost sm nodrag" onclick={() => (opened = null)}><span class="back"><Icon name="chev" size={12} /></span> {t("Back")}</button>
      <h1 class="ttl" title={opened.path ?? opened.name}>{opened.name}</h1>
      <span class="sp"></span>
      {#if opened.thread}<button class="btn ghost sm nodrag" onclick={() => openChat(opened!)}>{t("Open the chat")}</button>{/if}
    {:else}
      <h1>{t("Artifacts")}</h1>
      <span class="sp"></span>
      <label class="filter nodrag">
        <Icon name="search" size={13} />
        <input placeholder={t("Filter by name or address")} bind:value={q} />
      </label>
      <button class="btn ghost sm nodrag" onclick={refresh} title={t("Refresh")} aria-label={t("Refresh")}><Icon name="refresh" size={13} /></button>
    {/if}
  </header>
  {#if opened}
    <div class="viewer"><ArtifactView cur={refOf(opened)} /></div>
  {:else}
    <div class="main">
      {#if err}<div class="err selectable">{err}</div>{/if}
      {#if items === null}
        <div class="empty"><span class="spin"></span></div>
      {:else if !items.length}
        <div class="empty">
          <Icon name="file" size={28} />
          {#if q.trim()}<div>{t("Nothing matches")}</div>
          {:else}
            <div class="big">{t("No artifacts yet")}</div>
            <div class="muted">{t("Files the agents hand back with their replies, and the documents, pages and pictures they make on this Mac, gather here from every chat.")}</div>
          {/if}
        </div>
      {:else}
        <div class="grid">
          {#each items as a (itemKey(a))}
            <LibraryCard item={a} {stamp} onopen={open} onchat={openChat} />
          {/each}
        </div>
        {#if next}<div class="tail" use:inView={v => { if (v) loadMore(); }}>{#if more}<span class="spin"></span>{/if}</div>{/if}
      {/if}
    </div>
  {/if}
</section>

<style>
  .lib { flex: 1; min-width: 0; display: flex; flex-direction: column; background: var(--page); }
  header { height: 54px; display: flex; align-items: center; gap: 10px; padding: 0 20px 0 28px; border-bottom: 1px solid var(--line-soft); flex: none; min-width: 0; }
  h1 { font-size: 15px; margin: 0; }
  .ttl { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
  .sp { flex: 1; }
  .back { display: inline-grid; transform: rotate(180deg); }
  .filter { display: flex; align-items: center; gap: 6px; height: 28px; padding: 0 10px; border: 1px solid var(--line-soft); border-radius: 9px; color: var(--muted); width: min(280px, 40vw); }
  .filter input { flex: 1; min-width: 0; border: 0; background: transparent; outline: none; font-size: 12.5px; color: var(--ink); }
  .main { flex: 1; min-height: 0; overflow: auto; padding: 18px 20px 30px 28px; }
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 14px; }
  .tail { height: 40px; display: grid; place-items: center; }
  .viewer { flex: 1; min-height: 0; display: flex; }
  .empty { margin: 60px auto; display: grid; justify-items: center; gap: 10px; text-align: center; max-width: 440px; color: var(--ink-2); }
  .big { font-size: 16px; font-weight: 600; color: var(--ink); }
  .err { padding: 10px 12px; margin: 0 0 12px; border-radius: 12px; background: var(--crit-bg); color: var(--crit); font-size: 12.5px; white-space: pre-line; }
</style>
