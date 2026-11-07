<script lang="ts">
  import { Node, errText } from "../lib/api";
  import { tokens, money } from "../lib/format";

  let days = $state(7);
  let by = $state<"agent" | "model" | "day" | "session">("agent");
  let rows = $state<any[]>([]);
  let err = $state("");
  let loading = $state(true);
  async function load() {
    loading = true; err = "";
    try { rows = ((await Node.Usage(days, by)) ?? []) as any[]; } catch (e) { err = errText(e); }
    loading = false;
  }
  $effect(() => { days; by; load(); });
  const total = $derived(rows.reduce((a, r) => ({
    turns: a.turns + r.turns, input: a.input + r.input, output: a.output + r.output, cr: a.cr + r.cache_read, cw: a.cw + r.cache_write,
    cost: a.cost + (r.cost || 0) + (r.estimated || 0), cur: r.currency || r.estimated_currency || a.cur,
  }), { turns: 0, input: 0, output: 0, cr: 0, cw: 0, cost: 0, cur: "USD" }));
  const cache = $derived(total.input + total.cr + total.cw ? total.cr / (total.input + total.cr + total.cw) : 0);
  const maxTok = $derived(Math.max(1, ...rows.map(r => r.input + r.output + r.cache_read + r.cache_write)));
  const sorted = $derived(by === "day" ? [...rows].sort((a, b) => a.key.localeCompare(b.key)) : [...rows].sort((a, b) => (b.cost + (b.estimated || 0)) - (a.cost + (a.estimated || 0)) || b.total - a.total));
</script>

<section class="us scroll">
  <header class="drag">
    <h1>Usage</h1>
    <span class="sp"></span>
    <div class="seg nodrag">{#each [[1, "Today"], [7, "7 days"], [30, "30 days"], [0, "All"]] as [d, l]}<button class:on={days === d} onclick={() => (days = d as number)}>{l}</button>{/each}</div>
    <div class="seg nodrag">{#each ["agent", "model", "day", "session"] as b}<button class:on={by === b} onclick={() => (by = b as any)}>{b[0].toUpperCase() + b.slice(1)}</button>{/each}</div>
  </header>

  <div class="tiles">
    {#each [["Cost", money(total.cost, total.cur), rows.some(r => r.estimated) ? "~ includes estimates from prices" : ""], ["Turns", String(total.turns), ""], ["Tokens in / out", `${tokens(total.input + total.cr + total.cw)} / ${tokens(total.output)}`, ""], ["Cache hit", `${(cache * 100).toFixed(0)}%`, `${tokens(total.cr)} read from cache`]] as [k, v, s]}
      <div class="card tile"><div class="k muted">{k}</div><div class="v num">{loading ? "…" : v}</div>{#if s}<div class="s muted">{s}</div>{/if}</div>
    {/each}
  </div>

  {#if err}<div class="empty">{err}</div>
  {:else}
    <div class="card panel"><div class="inner">
      <table>
        <thead><tr><th>{by}</th><th class="r">turns</th><th>tokens</th><th class="r">cache</th><th class="r">cost</th></tr></thead>
        <tbody>
          {#each sorted as r (r.key)}
            {@const tok = r.input + r.output + r.cache_read + r.cache_write}
            <tr>
              <td class="key mono" title={r.key}>{r.key || "—"}</td>
              <td class="r num">{r.turns}</td>
              <td class="bar"><span class="b"><span class="i" style="width:{(r.input / maxTok) * 100}%"></span><span class="c" style="width:{(r.cache_read / maxTok) * 100}%"></span><span class="o" style="width:{(r.output / maxTok) * 100}%"></span></span><span class="num muted">{tokens(tok)}</span></td>
              <td class="r num">{(r.cache_ratio * 100).toFixed(0)}%</td>
              <td class="r num">{r.costed ? money(r.cost, r.currency) : ""}{r.estimated ? `${r.costed ? " + " : ""}~${money(r.estimated, r.estimated_currency)}` : ""}{!r.costed && !r.estimated ? "—" : ""}</td>
            </tr>
          {:else}
            <tr><td colspan="5" class="muted none">{loading ? "Loading…" : "No turns in this period"}</td></tr>
          {/each}
        </tbody>
      </table>
    </div></div>
    <div class="legend muted"><span class="sw i"></span>input <span class="sw c"></span>cache read <span class="sw o"></span>output</div>
  {/if}
</section>

<style>
  .us { flex: 1; min-width: 0; padding: 0 36px 36px; background: var(--page); }
  header { height: 60px; display: flex; align-items: center; gap: 10px; }
  h1 { font-size: 16px; font-weight: 600; margin: 0; }
  .sp { flex: 1; }
  .tiles { display: grid; grid-template-columns: repeat(4, 1fr); gap: 14px; margin: 8px 0 20px; }
  .tile { padding: 16px 18px; }
  .k { font-size: 11.5px; } .v { font-size: 20px; font-weight: 600; margin-top: 4px; letter-spacing: -.01em; } .s { font-size: 12px; margin-top: 2px; }
  table { width: 100%; border-collapse: collapse; font-size: 12.5px; }
  th { text-align: left; font-weight: 500; color: var(--muted); font-size: 12px; padding: 10px 14px; border-bottom: 1px solid var(--line-soft); text-transform: capitalize; }
  td { padding: 0 14px; height: 44px; border-bottom: 1px solid var(--line-soft); }
  tr:last-child td { border-bottom: 0; }
  .r { text-align: right; }
  .key { max-width: 280px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: 12.5px; }
  .bar { width: 40%; } .bar .num { font-size: 12px; margin-left: 8px; }
  .b { display: inline-flex; width: calc(100% - 60px); height: 8px; border-radius: 8px; background: var(--track); overflow: hidden; vertical-align: middle; }
  .i { background: var(--accent); } .c { background: color-mix(in srgb, var(--accent) 35%, var(--track)); } .o { background: var(--good); }
  .none { text-align: center; }
  .legend { display: flex; gap: 8px; align-items: center; font-size: 12px; padding: 10px 6px; }
  .sw { width: 10px; height: 10px; border-radius: 3px; display: inline-block; margin-left: 8px; }
  .sw.i { background: var(--accent); } .sw.c { background: color-mix(in srgb, var(--accent) 35%, var(--track)); } .sw.o { background: var(--good); }
</style>
