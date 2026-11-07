<script lang="ts">
  import { app, type Chat } from "../lib/state.svelte";
  import { ago, who } from "../lib/format";
  import Icon from "./Icon.svelte";

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
    <h1>Agents</h1>
    <span class="muted">{threads.length} tasks</span>
    <span class="sp"></span>
    <button class="btn primary sm nodrag" onclick={() => app.newTask()}><Icon name="plus" size={12} /> New task</button>
  </header>
  <div class="cols">
    {#each cols as col (col.key)}
      <div class="col">
        <div class="ch"><span class="dot {col.key === 'run' ? 'running' : col.key === 'fail' ? 'failed' : col.key === 'wait' ? 'queued' : 'done'}"></span>{col.title}<span class="muted num">{col.items.length}</span></div>
        <div class="cards scroll">
          {#each col.items as c (c.key)}
            <button class="card item" onclick={() => app.open(c)}>
              <div class="tt">{c.title || "Untitled"}</div>
              <div class="meta muted">
                <span><Icon name={c.kind === "cloud" ? "cloud" : "bot"} size={11} /> {agentName(c)}</span>
                {#if place(c)}<span class="mono">{place(c)}</span>{/if}
                <span class="sp"></span>
                {#if col.key === "wait"}<span class="chip ask">{asksByThread.get(c.key)} to approve</span>{/if}
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
  .cols { flex: 1; min-height: 0; display: grid; grid-template-columns: repeat(4, minmax(220px, 1fr)); gap: 14px; padding: 16px 20px; overflow-x: auto; }
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
</style>
