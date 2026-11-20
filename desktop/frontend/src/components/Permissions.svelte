<script lang="ts">
  import { focusOnMount } from "../lib/actions";
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node, errText } from "../lib/api";
  import Icon from "./Icon.svelte";
  import { blockedIn, ruleFor, approveText, type Blocked } from "../lib/blocked";
  import { sendNow } from "./genui/draft";
  import { clock } from "../lib/format";

  // /permissions, as Claude Code draws it in its terminal: the rules that let a tool run without
  // asking, make it ask, or refuse it, and the directories a session may reach besides its folder.
  // They are Claude Code's settings files (yours, the project's, the project's local one): the
  // agent reads them as a session starts. Through ACP there is no /permissions: the app edits them.
  let { folder }: { folder: string } = $props();

  type Src = { source: string; path: string; exists: boolean; allow: string[]; ask: string[]; deny: string[]; dirs: string[]; mode?: string; error?: string };
  type List = "allow" | "ask" | "deny" | "dirs";
  type Tab = List | "recent";
  const tabs: { k: Tab; label: string; about: string }[] = [
    { k: "recent", label: "Recently blocked", about: "What auto mode refused in this chat. Approving it adds no rule; a rule allows it from now on." },
    { k: "allow", label: "Allow", about: "Claude Code runs these without asking." },
    { k: "ask", label: "Ask", about: "Claude Code always asks before these, even where another rule allows them." },
    { k: "deny", label: "Deny", about: "Claude Code never runs these." },
    { k: "dirs", label: "Workspace", about: "Folders besides the chat's that Claude Code may read and edit." },
  ];
  const where: Record<string, { label: string; about: string }> = {
    local: { label: "Project, local", about: "Saved in .claude/settings.local.json: this Mac only, not committed" },
    project: { label: "Project", about: "Saved in .claude/settings.json: shared with the project's team" },
    user: { label: "User", about: "Saved in ~/.claude/settings.json: every project of yours" },
  };
  let tab = $state<Tab>(app.permissionsTab);
  let srcs = $state<Src[]>([]);
  let err = $state("");
  let rule = $state("");
  let dest = $state("local");
  let busy = $state(false);

  async function load() {
    try { srcs = ((await Node.PermissionRules(folder)) ?? []) as any; err = ""; }
    catch (e) { err = errText(e); }
  }
  $effect(() => { folder; load(); });

  const list = $derived<List>(tab === "recent" ? "allow" : tab);
  const rows = $derived(srcs.flatMap(s => (s[list] ?? []).map(r => ({ r, s }))));
  // The open chat's calls that auto mode refused (its turns, and a session's history before acpost).
  const blocked = $derived((app.rev, blockedIn([
    ...app.msgs.filter(m => app.turns[m.id]).map(m => [m.id, app.turns[m.id]] as [string, (typeof app.turns)[string]]),
    ...app.history.map((h, i) => ["history:" + i, h.turn] as [string, (typeof app.turns)[string]]),
  ])));
  let input = $state<HTMLInputElement>();
  // "Allow with a rule": the rule that would allow it, in the Allow tab's field, to be read and saved.
  function allowRule(b: Blocked) {
    tab = "allow";
    rule = ruleFor(b);
    requestAnimationFrame(() => input?.focus());
  }
  let approving = $state("");
  async function approve(b: Blocked) {
    approving = b.msg + ":" + b.id;
    try { if (await sendNow(approveText(b, t("I approve this action that auto mode blocked; run it again:")))) close(); }
    finally { approving = ""; }
  }
  const short = (p: string) => (app.home && p.startsWith(app.home) ? "~" + p.slice(app.home.length) : p);
  const examples: Record<List, string> = {
    allow: "Bash(npm run test:*), mcp__forgejo__search_repos, WebFetch(domain:github.com)",
    ask: "Bash(git push:*)",
    deny: "Read(./.env), Bash(rm -rf:*)",
    dirs: "../shared-lib",
  };

  async function edit(s: string, r: string, add: boolean) {
    busy = true;
    try { await Node.PermissionEdit(folder, s, list, r, add); await load(); if (add) rule = ""; }
    catch (e) { err = errText(e); }
    finally { busy = false; }
  }
  function close() { app.permissionsFor = null; }
  function key(e: KeyboardEvent) { if (e.key === "Escape") { e.preventDefault(); close(); } }
  const focus = (el: HTMLInputElement) => focusOnMount(el);
</script>

<svelte:window onkeydown={key} />
<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="shade" onclick={e => { if (e.target === e.currentTarget) close(); }}>
  <div class="card perms" role="dialog" aria-label={t("Permissions")}>
    <div class="head">
      <b>{t("Permissions")}</b>
      <span class="muted small mono" title={folder}>{short(folder)}</span>
      <span class="sp"></span>
      <button class="btn ghost icon sm" aria-label={t("Close")} onclick={close}><Icon name="x" size={13} /></button>
    </div>
    <div class="tabs" role="tablist">
      {#each tabs as x}
        <button role="tab" class="tab" class:on={tab === x.k} aria-selected={tab === x.k} onclick={() => (tab = x.k)}>
          {t(x.label)} <span class="muted num">{x.k === "recent" ? blocked.length : srcs.reduce((a, s) => a + (s[x.k as List]?.length ?? 0), 0)}</span>
        </button>
      {/each}
    </div>
    <div class="about muted">{t(tabs.find(x => x.k === tab)!.about)}</div>

    {#if tab === "recent"}
      <div class="list">
        {#each blocked as b (b.msg + ":" + b.id)}
          <div class="blk">
            <div class="bl-top">
              <span class="mono r selectable">{b.command || b.title}</span>
              <span class="muted num small">{clock(new Date(b.at).toISOString())}</span>
            </div>
            {#if b.reason}<div class="why muted">{t("Why: {r}", { r: b.reason })}</div>{/if}
            <div class="bl-acts">
              <button class="btn ghost sm" onclick={() => allowRule(b)} title={t("Puts the rule that would allow it in the Allow tab: read it, then save it")}>{t("Allow with a rule…")}</button>
              <button class="btn sm" disabled={!!approving} onclick={() => approve(b)} title={t("Tells the agent you approve it and to run it again; no rule is saved")}>{t("Approve and retry")}</button>
            </div>
          </div>
        {:else}
          <div class="none muted">{t("No recent blocks. Commands auto mode blocks will appear here.")}</div>
        {/each}
      </div>
    {:else}
    <form class="add" onsubmit={e => { e.preventDefault(); if (rule.trim()) edit(dest, rule, true); }}>
      <input class="input mono" bind:this={input} placeholder={tab === "dirs" ? t("A folder") : t("A rule, e.g. {e}", { e: examples[list].split(", ")[0] })} bind:value={rule} use:focus />
      <select class="input" bind:value={dest} title={t(where[dest]?.about ?? "")}>
        {#each srcs as s}<option value={s.source}>{t(where[s.source]?.label ?? s.source)}</option>{/each}
      </select>
      <button class="btn primary sm" disabled={busy || !rule.trim()}>{t("Add")}</button>
    </form>
    <div class="hint muted">{t(where[dest]?.about ?? "")}</div>

    <div class="list">
      {#each rows as { r, s } (s.source + "\u0000" + r)}
        <div class="rule">
          <span class="mono r selectable">{r}</span>
          <span class="chip" title={s.path}>{t(where[s.source]?.label ?? s.source)}</span>
          <button class="btn ghost icon sm" disabled={busy} title={t("Remove")} aria-label={t("Remove")} onclick={() => edit(s.source, r, false)}><Icon name="x" size={12} /></button>
        </div>
      {:else}
        <div class="none muted">{t("No rules here yet. For example: {e}", { e: examples[list] })}</div>
      {/each}
    </div>
    {/if}
    {#each srcs.filter(s => s.error) as s}<div class="err selectable">{short(s.path)}: {s.error}</div>{/each}
    {#if err}<div class="err selectable">{err}</div>{/if}
    <div class="foot muted">{t("Claude Code reads these as a session starts: a chat already going may take them from its next turn.")}</div>
  </div>
</div>

<style>
  .shade { position: fixed; inset: 0; z-index: 80; background: rgba(0,0,0,.18); display: grid; justify-items: center; align-items: start; padding-top: 10vh; }
  .perms { width: min(640px, 92vw); max-height: 78vh; display: flex; flex-direction: column; padding: 12px 14px; gap: 8px; }
  .head { display: flex; align-items: center; gap: 10px; min-width: 0; }
  .head .mono { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
  .sp { flex: 1; }
  .small { font-size: 11.5px; }
  .tabs { display: flex; gap: 4px; border-bottom: 1px solid var(--line-soft); }
  .tab { border: 0; background: transparent; padding: 6px 10px; font-size: 12.5px; color: var(--ink-2); border-bottom: 2px solid transparent; margin-bottom: -1px; }
  .tab.on { color: var(--ink); border-bottom-color: var(--accent); font-weight: 500; }
  .tab .num { font-size: 11px; margin-left: 2px; }
  .about, .hint, .foot { font-size: 11.5px; }
  .add { display: flex; gap: 6px; align-items: center; }
  .add input { flex: 1; min-width: 0; height: 30px; font-size: 12.5px; padding: 0 10px; }
  .add select { height: 30px; font-size: 12px; padding: 0 8px; flex: none; }
  .list { overflow: auto; min-height: 60px; display: grid; align-content: start; gap: 2px; border: 1px solid var(--line-soft); border-radius: 10px; padding: 4px; }
  .rule { display: flex; align-items: center; gap: 8px; padding: 4px 6px 4px 10px; border-radius: 8px; }
  .rule:hover { background: var(--hover); }
  .rule .r { flex: 1; min-width: 0; font-size: 12px; overflow-wrap: anywhere; }
  .none { padding: 12px; font-size: 12px; }
  .blk { display: grid; gap: 4px; padding: 8px 10px; border-radius: 8px; border-bottom: 1px solid var(--line-soft); }
  .blk:last-child { border-bottom: 0; }
  .bl-top { display: flex; gap: 8px; align-items: baseline; }
  .bl-top .r { flex: 1; min-width: 0; font-size: 12px; overflow-wrap: anywhere; white-space: pre-wrap; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 4; line-clamp: 4; overflow: hidden; }
  .why { font-size: 11.5px; }
  .bl-acts { display: flex; gap: 6px; justify-content: flex-end; }
  .err { padding: 6px 10px; border-radius: 8px; background: var(--crit-bg); color: var(--crit); font-size: 12px; }
</style>
