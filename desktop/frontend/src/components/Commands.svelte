<script lang="ts">
  import { untrack, type Snippet } from "svelte";
  import { t } from "../lib/i18n.svelte";
  import { Node, errText } from "../lib/api";
  import { dur } from "../lib/format";
  import { latest } from "../lib/latest";
  import { analytics } from "../lib/analytics.svelte";
  import { traceTurn, commandGroups, type TThread, type CommandRow, type Span } from "../lib/trace";
  import Dropdown from "./Dropdown.svelte";
  import Icon from "./Icon.svelte";

  // Commands: agents' calls grouped by what they ran (a shell command by its program, any other
  // call by its kind) — in one conversation, or in the 20 most recent.
  let { head }: { head: Snippet } = $props();

  let recent = $state<TThread[]>([]);
  let one = $state<TThread | null>(null);
  let scope = $state(analytics.thread); // "" for the recent ones
  let err = $state("");
  let loading = $state(true);

  const asked = latest();
  async function load() {
    const s = scope;
    loading = true; err = "";
    const done = await asked(
      async () => {
        const list = recent.length && !s ? recent : (((await Node.Traces("", 20)) ?? []) as any as TThread[]);
        const th = s ? (list.find(x => x.thread === s) ?? (((await Node.Traces(s, 0)) ?? []) as any as TThread[])[0] ?? null) : null;
        return { list, th };
      },
      r => { recent = r.list; one = r.th; },
      e => (err = errText(e)),
    );
    if (done) loading = false;
  }
  $effect(() => { scope; untrack(load); });

  const threads = $derived(scope ? (one ? [one] : []) : recent);
  const spans = $derived.by(() => {
    const now = Date.now();
    const out: Span[] = [];
    for (const th of threads) for (const m of th.messages) out.push(...traceTurn(m, m.events ?? [], th.messages, now).spans);
    return out;
  });
  type Key = "key" | "runs" | "total" | "avg" | "max" | "failures";
  let by = $state<Key>("total");
  const groups = $derived.by(() => {
    const g = commandGroups(spans);
    if (by === "total") return g;
    return [...g].sort((a, b) => (by === "key" ? a.key.localeCompare(b.key) : (b[by] as number) - (a[by] as number)));
  });
  const maxTotal = $derived(Math.max(1, ...groups.map(g => g.total)));
  const scopes = $derived([{ value: "", label: t("The 20 most recent chats") },
    ...(one && !recent.some(r => r.thread === one!.thread) ? [one] : []).concat(recent).map(th => ({ value: th.thread, label: th.title || t("Untitled") }))]);
  const cols: [Key, string, boolean][] = [["key", "Command", false], ["runs", "Runs", true], ["total", "Total", true], ["avg", "Average", true], ["max", "Slowest", true], ["failures", "Failures", true]];
  const cell = (g: CommandRow, k: Key) => (k === "runs" || k === "failures" ? String(g[k]) : dur(g[k] as number) || "0ms");
</script>

<section class="cm scroll">
  <header class="drag">
    {@render head()}
    <span class="sp"></span>
    <div class="nodrag"><Dropdown bind:value={scope} items={scopes} /></div>
    <button class="btn ghost icon nodrag" title={t("Refresh")} onclick={() => { recent = []; load(); }}><Icon name="refresh" size={14} /></button>
  </header>

  {#if err}<div class="empty">{err}</div>
  {:else}
    <div class="card panel"><div class="inner">
      <table>
        <thead><tr>{#each cols as [k, l, r]}<th class:r><button class="sort" class:on={by === k} onclick={() => (by = k)}>{t(l)}</button></th>{/each}<th class="barc"></th></tr></thead>
        <tbody>
          {#each groups as g (g.key)}
            <tr>
              <td class="key mono" title={g.key}>{g.key}</td>
              {#each cols.slice(1) as [k]}<td class="r num" class:bad={k === "failures" && g.failures > 0}>{cell(g, k)}</td>{/each}
              <td class="barc"><span class="b"><span class="i" style="width:{(g.total / maxTotal) * 100}%"></span></span></td>
            </tr>
          {:else}
            <tr><td colspan="7" class="muted none">{loading ? t("Loading…") : t("No calls yet")}</td></tr>
          {/each}
        </tbody>
      </table>
    </div></div>
  {/if}
</section>

<style>
  .cm { flex: 1; min-width: 0; padding: 0 36px 36px; background: var(--page); }
  header { height: 60px; display: flex; align-items: center; gap: 10px; }
  .sp { flex: 1; }
  table { width: 100%; border-collapse: collapse; font-size: 12.5px; }
  th { text-align: left; font-weight: 500; color: var(--muted); font-size: 12px; padding: 10px 14px; border-bottom: 1px solid var(--line-soft); }
  td { padding: 0 14px; height: 40px; border-bottom: 1px solid var(--line-soft); }
  tr:last-child td { border-bottom: 0; }
  .r { text-align: right; }
  .sort { border: 0; background: none; padding: 0; font: inherit; color: inherit; cursor: pointer; }
  .sort.on { color: var(--ink); }
  .key { max-width: 320px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .barc { width: 22%; }
  .b { display: inline-flex; width: 100%; height: 8px; border-radius: 8px; background: var(--track); overflow: hidden; vertical-align: middle; }
  .i { background: var(--good); }
  .bad { color: var(--crit); }
  .none { text-align: center; }
</style>
