<script lang="ts">
  import { untrack, type Snippet } from "svelte";
  import { t } from "../lib/i18n.svelte";
  import { Node, errText } from "../lib/api";
  import { dur, ago, clock, who } from "../lib/format";
  import { latest } from "../lib/latest";
  import { analytics } from "../lib/analytics.svelte";
  import { threadSum, threadTurns, sortSums, addPhases, isFinal, type TThread } from "../lib/trace";
  import PhaseBar from "./PhaseBar.svelte";
  import TraceTurn from "./TraceTurn.svelte";
  import Icon from "./Icon.svelte";

  // Traces: the recent conversations and where their time went; one opened, its turns; a turn
  // opened, its timeline (TraceTurn).
  let { head }: { head: Snippet } = $props();

  let list = $state<TThread[]>([]);
  let open = $state<TThread | null>(null);
  let err = $state("");
  let loading = $state(true);
  let sort = $state<"recent" | "time">("recent");
  let now = $state(Date.now());

  const asked = latest();
  async function load(quiet = false) {
    const thread = analytics.thread;
    if (!quiet) { loading = true; err = ""; }
    const done = await asked(
      () => Node.Traces(thread, thread ? 0 : 20) as Promise<any>,
      (r: TThread[]) => {
        now = Date.now();
        if (thread) open = r?.[0] ?? null; else list = r ?? [];
        err = "";
      },
      e => { if (!quiet) err = errText(e); },
    );
    if (done) loading = false;
  }
  $effect(() => { analytics.thread; untrack(() => { open = null; load(); }); });

  const sums = $derived(sortSums(list.map(th => threadSum(th, now)), sort));
  const all = $derived(sums.reduce((a, s) => addPhases(a, s.phases), { queue: 0, model: 0, tools: 0, approval: 0, agents: 0 }));
  const turns = $derived(open ? threadTurns(open, now) : []);
  // What runs (a turn, or one it asked) is read again.
  const running = $derived(analytics.thread ? !!open?.messages.some(m => !isFinal(m.state)) : sums.some(s => s.running));
  const openCut = $derived(open ? threadSum(open, now).cut : false);
  // What runs is read again every few seconds, and its clock moves each second.
  $effect(() => {
    if (!running) return;
    const tick = setInterval(() => (now = Date.now()), 1000);
    const again = setInterval(() => load(true), 4000);
    return () => { clearInterval(tick); clearInterval(again); };
  });

  function openThread(id: string) { analytics.turn = ""; analytics.thread = id; }
  function back() {
    if (analytics.turn) analytics.turn = "";
    else analytics.thread = "";
  }
  const title = $derived(open?.title || t("Untitled"));
</script>

<section class="tr scroll">
  <header class="drag">
    {@render head()}
    <span class="sp"></span>
    {#if !analytics.thread}
      <div class="seg nodrag">{#each [["recent", "Recent"], ["time", "Most time"]] as [k, l]}<button class:on={sort === k} onclick={() => (sort = k as any)}>{t(l)}</button>{/each}</div>
    {/if}
    <button class="btn ghost icon nodrag" title={t("Refresh")} onclick={() => load()}><Icon name="refresh" size={14} /></button>
  </header>

  {#if analytics.thread}
    <nav class="crumbs">
      <button class="lnk" onclick={() => { analytics.thread = ""; analytics.turn = ""; }}>{t("Traces")}</button>
      <span class="muted">/</span>
      {#if analytics.turn}<button class="lnk" onclick={back}>{title}</button><span class="muted">/</span><span>{t("Turn")}</span>
      {:else}<span class="cur">{title}</span>{/if}
    </nav>
  {/if}

  {#if err}<div class="empty">{err}</div>
  {:else if analytics.thread && analytics.turn && open}
    <TraceTurn thread={open} id={analytics.turn} {now} onchange={() => load(true)} />
  {:else if analytics.thread}
    {#if !open}<div class="empty muted">{loading ? t("Loading…") : t("No turns in this conversation")}</div>
    {:else}
      <div class="card panel"><div class="inner">
        <table>
          <thead><tr><th>{t("Agent")}</th><th>{t("Sent")}</th><th class="r">{t("Active")}</th><th class="r">{t("Calls")}</th><th class="r">{t("Errors")}</th><th class="pbc">{t("Where the time went")}</th></tr></thead>
          <tbody>
            {#each [...turns].reverse() as { msg, trace } (msg.id)}
              <tr class="click" onclick={() => (analytics.turn = msg.id)}>
                <td class="mono">{who(msg.to).name}{#if trace.running}<span class="chip warn">{t("running")}</span>{:else if msg.state !== "done"}<span class="chip" class:crit={msg.state === "failed"}>{t(msg.state)}</span>{/if}</td>
                <td class="muted num">{clock(msg.created_at)}</td>
                <td class="r num">{dur(trace.active) || "—"}</td>
                <td class="r num">{trace.calls}</td>
                <td class="r num" class:bad={trace.errors > 0}>{trace.errors}</td>
                <td class="pbc"><PhaseBar phases={trace.phases} /></td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div></div>
      {#if openCut}<div class="note muted">{t("Older turns or some events are not shown")}</div>{/if}
    {/if}
  {:else}
    <div class="card panel"><div class="inner">
      <table>
        <thead><tr><th>{t("Chat")}</th><th class="r">{t("Turns")}</th><th class="r">{t("Active")}</th><th class="r">{t("Calls")}</th><th class="r">{t("Errors")}</th><th class="pbc">{t("Where the time went")}</th><th class="r">{t("Updated")}</th></tr></thead>
        <tbody>
          {#each sums as s (s.thread)}
            <tr class="click" onclick={() => openThread(s.thread)}>
              <td class="key" title={s.title}>{s.title || t("Untitled")}{#if s.running}<span class="chip warn">{t("running")}</span>{/if}{#if s.cut}<span class="chip" title={t("Older turns or some events are not shown")}>{t("incomplete")}</span>{/if}</td>
              <td class="r num">{s.turns}</td>
              <td class="r num">{dur(s.active) || "—"}</td>
              <td class="r num">{s.calls}</td>
              <td class="r num" class:bad={s.errors > 0}>{s.errors}</td>
              <td class="pbc"><PhaseBar phases={s.phases} /></td>
              <td class="r muted num">{ago(new Date(s.updated))}</td>
            </tr>
          {:else}
            <tr><td colspan="7" class="muted none">{loading ? t("Loading…") : t("No conversations yet")}</td></tr>
          {/each}
        </tbody>
      </table>
    </div></div>
    {#if sums.length}<div class="all"><div class="muted k">{t("All of them")}</div><PhaseBar phases={all} legend /></div>{/if}
  {/if}
</section>

<style>
  .tr { flex: 1; min-width: 0; padding: 0 36px 36px; background: var(--page); }
  header { height: 60px; display: flex; align-items: center; gap: 10px; }
  .sp { flex: 1; }
  .crumbs { display: flex; gap: 8px; align-items: center; font-size: 13px; margin: 0 0 14px; min-width: 0; }
  .crumbs .cur, .crumbs .lnk { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; max-width: 420px; }
  .lnk { border: 0; background: none; padding: 0; color: var(--accent); font: inherit; cursor: pointer; }
  .lnk:hover { text-decoration: underline; }
  table { width: 100%; border-collapse: collapse; font-size: 12.5px; }
  th { text-align: left; font-weight: 500; color: var(--muted); font-size: 12px; padding: 10px 14px; border-bottom: 1px solid var(--line-soft); }
  td { padding: 0 14px; height: 42px; border-bottom: 1px solid var(--line-soft); }
  tr:last-child td { border-bottom: 0; }
  tr.click { cursor: pointer; } tr.click:hover td { background: var(--hover); }
  td .chip { margin-left: 8px; }
  .r { text-align: right; }
  .key { max-width: 320px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .pbc { width: 26%; min-width: 140px; }
  .bad { color: var(--crit); }
  .none { text-align: center; }
  .all { margin-top: 16px; padding: 0 6px; } .all .k { font-size: 12px; margin-bottom: 6px; }
  .note { font-size: 12px; padding: 10px 6px; }
</style>
