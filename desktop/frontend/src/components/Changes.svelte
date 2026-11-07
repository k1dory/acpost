<script lang="ts">
  import { t, plural } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import { Node, errText } from "../lib/api";
  import { splitDiff } from "../lib/turn";
  import { who } from "../lib/format";
  import DiffLines from "./DiffLines.svelte";
  import Dropdown from "./Dropdown.svelte";

  let data = $state<any>(null);
  let err = $state("");
  let loading = $state(false);
  let pick = $state("");
  let agent = $state("");
  const root = $derived(app.msgs.find(m => m.depth === 0 && m.to.startsWith("agent:"))?.id ?? app.focus);

  async function load(id: string) {
    if (!id) return;
    loading = true; err = "";
    try { data = await Node.Changes(id); } catch (e) { err = errText(e); data = null; }
    loading = false;
  }
  $effect(() => { load(root); });
  $effect(() => { app.msgs.map(m => m.state).join(); if (root) load(root); });

  const agents = $derived([...new Set((data?.turns ?? []).map((t: any) => t.agent))] as string[]);
  const turns = $derived((data?.turns ?? []).filter((t: any) => !agent || t.agent === agent));
  // One agent: its turns' diffs; all: the repository's diff over the session.
  const files = $derived.by(() => {
    if (agent) return turns.flatMap((t: any) => splitDiff(t.diff ?? "").map(f => ({ ...f, repo: t.repo })));
    return (data?.repos ?? []).flatMap((r: any) => splitDiff(r.diff ?? "").map(f => ({ ...f, repo: r.repo })));
  });
  const stats = $derived.by(() => {
    const m = new Map<string, { added: number; removed: number; status: string }>();
    for (const r of agent ? turns : data?.repos ?? []) for (const f of (r.files ?? []) as any[]) {
      const s = m.get(f.path) ?? { added: 0, removed: 0, status: f.status };
      s.added += Math.max(0, f.added); s.removed += Math.max(0, f.removed); s.status = f.status;
      m.set(f.path, s);
    }
    return m;
  });
  const shown = $derived(files.find((f: any) => f.path === pick) ?? files[0]);
</script>

<div class="ch">
  <div class="tools">
    <Dropdown bind:value={agent} items={[{ value: "", label: t("All agents") }, ...agents.map(a => ({ value: a, label: who(a).name }))]} />
    <span class="muted num">{plural(stats.size, "file", "files")}</span>
    <span class="sp"></span>
    <button class="btn sm" onclick={() => load(root)}>{t("Refresh")}</button>
  </div>
  {#if loading && !data}
    <div class="pad"><div class="sk" style="height:14px;width:70%"></div></div>
  {:else if err}
    <div class="empty">{err}</div>
  {:else if !files.length}
    <div class="empty">{t("No changes in this session yet.")}</div>
  {:else}
    <div class="split">
      <div class="tree scroll">
        {#each [...stats] as [path, s]}
          <button class="f" class:on={shown?.path === path} onclick={() => (pick = path)}>
            <span class="stt mono st-{s.status[0]}">{s.status[0]}</span>
            <span class="p mono" title={path}>{path.split("/").pop()}<span class="muted dir">{path.includes("/") ? " " + path.slice(0, path.lastIndexOf("/")) : ""}</span></span>
            <span class="mono n"><span class="plus">+{s.added}</span> <span class="minus">−{s.removed}</span></span>
          </button>
        {/each}
      </div>
      <div class="view scroll">
        {#if shown}<div class="vh mono">{shown.path}</div><DiffLines lines={shown.lines} />{/if}
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
  .vh { font-size: 12px; padding: 6px 10px; border-bottom: 1px solid var(--line-soft); position: sticky; top: 0; background: var(--card); }
</style>
