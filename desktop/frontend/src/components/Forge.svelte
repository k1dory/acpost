<script lang="ts">
  import { app } from "../lib/state.svelte";
  import { Node, errText } from "../lib/api";
  import { ago, dur } from "../lib/format";
  import Icon from "./Icon.svelte";

  let info = $state<any>(null);
  let pulls = $state<any[]>([]);
  let checks = $state<any[]>([]);
  let err = $state("");
  let log = $state<{ name: string; text: string; error?: string } | null>(null);
  let level = $state<"all" | "info" | "warn" | "error">("all");
  let busy = $state(false);
  const agent = $derived(app.focusAgent());

  async function load() {
    err = ""; busy = true;
    try {
      info = await Node.Forge(agent);
      [pulls, checks] = await Promise.all([Node.Pulls(agent, "open") as any, Node.Checks(agent, "") as any]);
    } catch (e) { err = errText(e); }
    busy = false;
  }
  $effect(() => { if (agent) load(); });

  async function openLog(job: any, check: any) {
    log = { name: job.name, text: "" };
    try { log = { name: job.name, text: await Node.CILog(agent, job.id, check.url ?? "") }; } catch (e) { log = { name: job.name, text: "", error: errText(e) }; }
  }
  // Levels as CI writes them: ##[error], ERROR, ANSI red; ##[warning], WARN, yellow.
  function lv(l: string): "error" | "warn" | "info" {
    if (/##\[error\]|\berror\b|\bFAIL\b|\x1b\[31m|panic:/i.test(l)) return "error";
    if (/##\[warning\]|\bwarn(ing)?\b|\x1b\[33m/i.test(l)) return "warn";
    return "info";
  }
  const lines = $derived((log?.text ?? "").split("\n").map(t => ({ t: t.replace(/\x1b\[[0-9;]*m/g, ""), l: lv(t) })).filter(x => level === "all" || x.l === level));
  function cls(s: string) { return /success|passed|done/.test(s) ? "good" : /fail|error|cancel/.test(s) ? "crit" : /running|pending|queued|in_progress|waiting/.test(s) ? "warn" : ""; }
  function took(c: any) { const a = Date.parse(c.started), b = Date.parse(c.finished); return a > 0 && b > 0 ? dur(b - a) : ""; }
</script>

<div class="fg scroll">
  {#if !agent}
    <div class="empty">Open a conversation with an agent on this node to see its repository.</div>
  {:else if err}
    <div class="empty">{err}<button class="btn sm" onclick={load}>Retry</button></div>
  {:else if !info}
    <div class="pad"><div class="sk" style="height:14px;width:60%"></div></div>
  {:else if log}
    <div class="lh">
      <button class="btn ghost sm" onclick={() => (log = null)}>← Back</button>
      <b class="ln">{log.name}</b>
      <span class="sp"></span>
      <div class="seg" class:hidden={!!log.error}>{#each ["all", "info", "warn", "error"] as l}<button class:on={level === l} onclick={() => (level = l as any)}>{l === "all" ? "All" : l === "info" ? "Info" : l === "warn" ? "Warnings" : "Errors"}</button>{/each}</div>
    </div>
    {#if log.error}
      <div class="empty">
        <div class="selectable">{log.error}</div>
        {#if /rate limit|401|403|token/i.test(log.error)}<div>Keep a token for the forge: <code class="selectable">acpost forge login {info.repo.host}</code></div>{/if}
      </div>
    {:else if !log.text}
      <div class="pad"><div class="sk" style="height:12px;width:80%"></div></div>
    {:else}
      <div class="log mono selectable">
        {#each lines as x}<div class="ll {x.l}">{x.t}</div>{/each}
      </div>
    {/if}
  {:else}
    <div class="repo">
      <Icon name="pr" /><a href={info.repo.web} target="_blank" rel="noreferrer">{info.repo.path}</a>
      <span class="chip">{info.server.kind}{info.server.version ? ` ${info.server.version}` : ""}</span>
      {#if info.branch}<span class="chip mono">{info.branch}</span>{/if}
      {#if !info.authed}<span class="chip warn" title={`acpost forge login ${info.repo.host}`}>no token</span>{/if}
      <span class="sp"></span><button class="btn ghost sm" onclick={load} disabled={busy}><Icon name="refresh" size={13} /></button>
    </div>

    <div class="h">Pull requests</div>
    <div class="inner">
      {#each pulls as p (p.number)}
        <a class="row" href={p.url} target="_blank" rel="noreferrer">
          <span class="mono muted">#{p.number}</span><span class="t">{p.title}</span>
          {#if p.draft}<span class="chip">draft</span>{/if}
          <span class="mono muted small">{p.head} → {p.base}</span><span class="muted small">{ago(p.updated)}</span>
        </a>
      {:else}<div class="none muted">No open pull requests</div>{/each}
    </div>

    <div class="h">Checks {#if info.branch}<span class="muted">on {info.branch}</span>{/if}</div>
    <div class="inner">
      {#each checks as c (c.id)}
        <div class="check">
          <div class="row"><span class="chip {cls(c.status)}">{c.status}</span><span class="t">{c.name}</span><span class="muted small num">{took(c)}</span></div>
          {#each c.jobs ?? [] as j (j.id)}
            <button class="row job" class:fail={cls(j.status) === "crit"} onclick={() => openLog(j, c)}>
              <span class="dot {cls(j.status) === 'good' ? 'done' : cls(j.status) === 'crit' ? 'failed' : cls(j.status) === 'warn' ? 'running' : ''}"></span>
              <span class="t">{j.name}</span><span class="muted small">log</span>
            </button>
          {/each}
          {#if !(c.jobs ?? []).length && c.logs_by_jobs === false && c.url}<a class="row job" href={c.url} target="_blank" rel="noreferrer"><span class="t muted">Open on the forge</span></a>{/if}
        </div>
      {:else}<div class="none muted">No CI runs for this branch</div>{/each}
    </div>
  {/if}
</div>

<style>
  .fg { height: 100%; padding: 10px; }
  .pad { padding: 10px; }
  .repo { display: flex; align-items: center; gap: 8px; padding: 4px 4px 10px; flex-wrap: wrap; }
  .repo a { color: var(--ink); font-weight: 600; text-decoration: none; }
  .sp { flex: 1; }
  .h { font-size: 12px; font-weight: 600; color: var(--muted); padding: 10px 6px 6px; text-transform: uppercase; letter-spacing: .02em; }
  .inner { border: 1px solid var(--line-soft); border-radius: var(--r-card); background: var(--card); overflow: hidden; }
  .row { display: flex; align-items: center; gap: 9px; padding: 0 14px; min-height: 48px; border-bottom: 1px solid var(--line-soft); color: inherit; text-decoration: none; width: 100%; border-left: 0; border-right: 0; border-top: 0; background: transparent; text-align: left; font-size: 13.5px; }
  .row:last-child { border-bottom: 0; }
  a.row:hover, button.row:hover { background: var(--hover); }
  .row.job { min-height: 36px; padding-left: 28px; font-size: 13px; }
  .row.job.fail { background: var(--crit-bg); color: var(--crit); }
  .t { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .small { font-size: 12px; }
  .none { padding: 14px; font-size: 13px; }
  .lh { display: flex; align-items: center; gap: 8px; padding-bottom: 8px; }
  .lh .btn { flex: none; white-space: nowrap; }
  .ln { min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .lh .seg { flex: none; }
  .hidden { visibility: hidden; }
  .log { font-size: 12px; line-height: 1.5; border: 1px solid var(--line-soft); border-radius: var(--r-card); background: var(--card); padding: 10px 0; overflow: auto; }
  .ll { white-space: pre-wrap; word-break: break-all; padding: 0 12px; }
  .ll.error { background: var(--crit-bg); color: var(--crit); }
  .ll.warn { color: var(--warn); }
</style>
