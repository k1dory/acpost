<script lang="ts">
  // A table from a reply: sorted by a click on a column, narrowed by a filter, in the page (no
  // question to the agent). How it was left is kept (keep).
  import { untrack } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import { fmt, sortRows, filterRows, numeric, type Table } from "../../lib/genui";
  let { c, keep }: { c: Table; keep: Record<string, unknown> } = $props();

  // As it was left: a mounted block's props do not change (Markdown.svelte mounts it anew).
  let sort = $state(untrack(() => (keep.sort as { col: number; dir: 1 | -1 } | undefined) ?? null));
  let q = $state(untrack(() => (keep.q as string | undefined) ?? ""));
  const nums = $derived(c.columns.map((_, i) => numeric(c.rows, i)));
  const shown = $derived.by(() => {
    const rows = filterRows(c.rows, q);
    return sort ? sortRows(rows, sort.col, sort.dir) : rows;
  });

  // Ascending, descending, then as the agent gave it.
  function by(col: number) {
    sort = sort?.col !== col ? { col, dir: 1 } : sort.dir === 1 ? { col, dir: -1 } : null;
    keep.sort = sort ?? undefined;
  }
  function filter(e: Event) {
    q = (e.target as HTMLInputElement).value;
    keep.q = q;
  }
  const cell = (v: string | number | null) => v === null ? "" : typeof v === "number" ? fmt(v) : v;
</script>

<div class="tbl">
  {#if c.rows.length > 5}
    <div class="bar">
      <input class="input" type="search" value={q} oninput={filter} placeholder={t("Filter")} aria-label={t("Filter")} />
      <span class="muted">{t("{n} of {m} rows", { n: shown.length, m: c.rows.length })}</span>
    </div>
  {/if}
  <div class="wrap">
    <table>
      <thead>
        <tr>
          {#each c.columns as col, i}
            <th class:num={nums[i]} aria-sort={sort?.col === i ? (sort.dir === 1 ? "ascending" : "descending") : "none"}>
              <button type="button" onclick={() => by(i)} title={t("Sort by {col}", { col })}>{col}<span class="arrow">{sort?.col === i ? (sort.dir === 1 ? "↑" : "↓") : ""}</span></button>
            </th>
          {/each}
        </tr>
      </thead>
      <tbody>
        {#each shown as row}
          <tr>{#each row as v, i}<td class:num={nums[i]}>{cell(v)}</td>{/each}</tr>
        {:else}
          <tr><td class="none" colspan={c.columns.length}>{t("No rows")}</td></tr>
        {/each}
      </tbody>
    </table>
  </div>
</div>

<style>
  .bar { display: flex; align-items: center; gap: 10px; margin-bottom: 8px; }
  .bar .input { max-width: 220px; height: 26px; font-size: 12.5px; padding: 0 9px; }
  .bar .muted { font-size: 12px; }
  .wrap { overflow: auto; max-height: 420px; border: 1px solid var(--line-soft); border-radius: 10px; }
  .wrap table { border-collapse: collapse; width: 100%; font-size: 12.5px; display: table; margin: 0; }
  .wrap th, .wrap td { border: 0; border-bottom: 1px solid var(--line-soft); padding: 5px 10px; text-align: left; vertical-align: top; }
  .wrap tbody tr:last-child td { border-bottom: 0; }
  .wrap th { position: sticky; top: 0; background: var(--sel); padding: 0; font-weight: 600; }
  th button { all: unset; display: flex; gap: 4px; width: 100%; box-sizing: border-box; padding: 5px 10px; cursor: default; white-space: nowrap; }
  th.num button { justify-content: flex-end; }
  th button:hover { background: var(--hover); }
  th button:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
  .arrow { color: var(--muted); min-width: 8px; }
  .wrap .num { text-align: right; font-variant-numeric: tabular-nums; }
  .wrap .none { color: var(--muted); text-align: center; }
</style>
