<script lang="ts">
  import { t, plural } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node, errText } from "../lib/api";
  import { splitDiff } from "../lib/turn";
  import { who, clock } from "../lib/format";
  import { latest } from "../lib/latest";
  import { changeRoots, mergeChanges, joinFiles } from "../lib/changes";
  import DiffLines from "./DiffLines.svelte";
  import Dropdown from "./Dropdown.svelte";

  let data = $state<any>(null);
  let err = $state("");
  let loading = $state(false);
  let pick = $state("");
  let agent = $state("");
  // The whole chat's: each root of its tree is asked (each message of the person is one: a later
  // one is not a child of the first; so is a task delegated here, its parent on another node),
  // and the answers put together.
  const roots = $derived(changeRoots(app.msgs, app.focus));

  // Another chat's changes asked meanwhile: the answer for this one is the one kept.
  const changes = latest();
  async function load(ids: string[]) {
    if (!ids.length) {
      // No message here (a new task): nothing is this chat's, and an answer still coming is not.
      changes(async () => null, () => {});
      data = null; err = ""; loading = false;
      return;
    }
    loading = true; err = "";
    if (await changes(() => Promise.all(ids.map(id => Node.Changes(id) as any)), d => (data = mergeChanges(d)), e => { err = errText(e); data = null; })) loading = false;
  }
  // Once on opening, and again as a turn of the chat changes state (what it changed is then known).
  $effect(() => { app.msgs.map(m => m.state).join(); load(roots); });
  // Another chat (or a new task): what was shown and chosen is the last one's.
  const context = $derived(app.thread || app.chat?.key || "");
  let shownFor = app.thread || app.chat?.key || "";
  $effect(() => {
    const c = context;
    if (c === shownFor) return;
    shownFor = c;
    data = null; err = ""; pick = ""; agent = ""; version = -1; at = null;
  });

  const agents = $derived([...new Set((data?.turns ?? []).map((t: any) => t.agent))] as string[]);
  const turns = $derived((data?.turns ?? []).filter((t: any) => !agent || t.agent === agent));
  // One agent: its turns' diffs; all: the repository's diff over the session.
  const files = $derived.by(() => {
    if (agent) return joinFiles(turns.flatMap((t: any) => splitDiff(t.diff ?? "").map(f => ({ ...f, repo: t.repo }))));
    return joinFiles((data?.repos ?? []).flatMap((r: any) => splitDiff(r.diff ?? "").map(f => ({ ...f, repo: r.repo }))));
  });
  // A file is its repository and its path (two repositories may have the same path).
  const keyOf = (repo: string, path: string) => repo + "\u0000" + path;
  const stats = $derived.by(() => {
    const m = new Map<string, { path: string; repo: string; added: number; removed: number; status: string }>();
    for (const r of agent ? turns : data?.repos ?? []) for (const f of (r.files ?? []) as any[]) {
      const k = keyOf(r.repo, f.path);
      const s = m.get(k) ?? { path: f.path, repo: r.repo, added: 0, removed: 0, status: f.status };
      s.added += Math.max(0, f.added); s.removed += Math.max(0, f.removed); s.status = f.status;
      m.set(k, s);
    }
    return m;
  });
  const repos = $derived(new Set([...stats.values()].map(s => s.repo)).size);
  // A file listed without a diff (an edit too long to keep) is shown all the same, saying so.
  const shown = $derived.by(() => {
    const k = stats.has(pick) ? pick : stats.keys().next().value ?? "";
    const s = stats.get(k);
    return files.find((f: any) => keyOf(f.repo, f.path) === k) ?? (s ? { repo: s.repo, path: s.path, lines: [] as any[] } : files[0]);
  });

  // A file's versions: the turns that changed it, in order — each with its own diff of the file
  // and, on request, the whole file as that turn left it.
  let version = $state<number>(-1); // -1: the whole diff
  let at = $state<{ text?: string; err?: string } | null>(null);
  const versions = $derived.by(() => {
    if (!shown) return [];
    return ((data?.turns ?? []) as any[])
      .filter(t => t.repo === shown.repo && (t.files ?? []).some((f: any) => f.path === shown.path))
      .sort((a, b) => Date.parse(a.at) - Date.parse(b.at))
      .map(t => ({ turn: t, file: (t.files as any[]).find(f => f.path === shown.path), lines: splitDiff(t.diff ?? "").filter(f => f.path === shown.path).flatMap(f => f.lines) }));
  });
  let asked = 0; // the file's text asked last: an earlier answer is dropped
  $effect(() => { shown?.path; shown?.repo; version = -1; at = null; asked++; });
  async function fileAt(v: (typeof versions)[number]) {
    const my = ++asked;
    at = { text: "" };
    try {
      const text = await Node.FileAt(v.turn.repo, v.turn.after, shown!.path);
      if (my === asked) at = { text };
    } catch (e) { if (my === asked) at = { err: errText(e) }; }
  }
</script>

<div class="ch">
  <div class="tools">
    <Dropdown bind:value={agent} items={[{ value: "", label: t("All agents") }, ...agents.map(a => ({ value: a, label: who(a).name }))]} />
    <span class="muted num">{plural(stats.size, "file", "files")}</span>
    <span class="sp"></span>
    <button class="btn sm" onclick={() => load(roots)}>{t("Refresh")}</button>
  </div>
  {#if loading && !data}
    <div class="pad"><div class="sk" style="height:14px;width:70%"></div></div>
  {:else if err}
    <div class="empty">{err}</div>
  {:else if !files.length && !stats.size}
    <div class="empty">{t("No changes in this session yet.")}</div>
  {:else}
    <div class="split">
      <div class="tree scroll">
        {#each [...stats] as [k, s]}
          {@const path = s.path}
          <button class="f" class:on={shown && keyOf(shown.repo, shown.path) === k} onclick={() => (pick = k)} title={repos > 1 ? s.repo + "/" + path : path}>
            <span class="stt mono st-{s.status[0]}">{s.status[0]}</span>
            <span class="p mono" title={path}>{path.split("/").pop()}<span class="muted dir">{path.includes("/") ? " " + path.slice(0, path.lastIndexOf("/")) : ""}</span></span>
            <span class="mono n"><span class="plus">+{s.added}</span> <span class="minus">−{s.removed}</span></span>
          </button>
        {/each}
      </div>
      <div class="view scroll">
        {#if shown}
          <div class="vh">
            <span class="mono">{shown.path}</span>
            {#if versions.length > 1 || (versions.length === 1 && agent === "")}
              <span class="vers">
                <button class="vb" class:on={version === -1} onclick={() => { version = -1; at = null; asked++; }}>{t("The whole diff")}</button>
                {#each versions as v, i}
                  <button class="vb" class:on={version === i} onclick={() => { version = i; at = null; asked++; }} title={who(v.turn.agent).name + " · " + clock(v.turn.at)}>
                    v{i + 1} <span class="muted">{who(v.turn.agent).name} {clock(v.turn.at)}</span>
                  </button>
                {/each}
              </span>
            {/if}
          </div>
          {#if version >= 0 && versions[version]}
            {@const v = versions[version]}
            <div class="vbar">
              <span class="mono n"><span class="plus">+{Math.max(0, v.file?.added ?? 0)}</span> <span class="minus">−{Math.max(0, v.file?.removed ?? 0)}</span></span>
              <span class="sp"></span>
              {#if v.file?.status !== "D" && v.turn.after}<button class="btn ghost sm" onclick={() => { if (at) { at = null; asked++; } else fileAt(v); }}>{at ? t("This version") : t("File at this version")}</button>{/if}
            </div>
            {#if at}
              {#if at.err}<div class="empty">{at.err}</div>{:else}<pre class="whole selectable">{at.text}</pre>{/if}
            {:else}<DiffLines lines={v.lines} />{/if}
          {:else if shown.lines.length}<DiffLines lines={shown.lines} />
          {:else}<div class="empty">{t("No diff is kept for this file (binary, or an edit too long): see the file itself.")}</div>{/if}
        {/if}
      </div>
    </div>
  {/if}
</div>

<style>
  .ch { height: 100%; display: flex; flex-direction: column; }
  .tools { display: flex; align-items: center; gap: 8px; padding: 8px; border-bottom: 1px solid var(--line-soft); }
  .sel { width: auto; height: 28px; padding: 0 8px; border-radius: 10px; font-size: 13px; }
  .sp { flex: 1; }
  .pad { padding: 16px; }
  .split { flex: 1; min-height: 0; display: grid; grid-template-rows: minmax(90px, 30%) 1fr; }
  .tree { border-bottom: 1px solid var(--line-soft); padding: 4px; }
  .f { width: 100%; display: flex; gap: 8px; align-items: center; border: 0; background: transparent; padding: 4px 8px; border-radius: 9px; text-align: left; font-size: 12.5px; }
  .f:hover { background: var(--hover); } .f.on { background: var(--sel); }
  .p { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .dir { font-size: 11.5px; }
  .stt { width: 14px; font-weight: 600; } .st-A { color: var(--add-ink); } .st-D { color: var(--del-ink); } .st-M { color: var(--warn); }
  .n { font-size: 11.5px; } .plus { color: var(--add-ink); } .minus { color: var(--del-ink); }
  .vers { display: flex; gap: 4px; flex-wrap: wrap; margin-top: 4px; }
  .vb { border: 1px solid var(--line-soft); background: transparent; border-radius: 7px; font: 500 11px var(--sans); padding: 2px 7px; color: var(--ink-2); }
  .vb.on { background: var(--sel); color: var(--ink); border-color: transparent; }
  .vbar { display: flex; align-items: center; gap: 8px; padding: 4px 10px; border-bottom: 1px solid var(--line-soft); }
  .whole { margin: 0; padding: 10px 12px; font: 11.5px/1.5 var(--mono); white-space: pre; overflow: auto; }
  /* Above the diff (its line numbers are sticky too): opaque, on top. */
  .vh { display: grid; gap: 6px; font-size: 12px; padding: 8px 10px; border-bottom: 1px solid var(--line-soft); position: sticky; top: 0; z-index: 5; background: var(--card); box-shadow: 0 1px 0 var(--line-soft); }
  .vh .mono { font-weight: 600; }
</style>
