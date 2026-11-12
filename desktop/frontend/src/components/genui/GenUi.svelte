<script lang="ts">
  // A ```ui block of a reply, drawn (Markdown.svelte mounts it where the block is): its components,
  // each in a card. The block was checked (lib/genui.ts): every text here is shown as text.
  import { t } from "../../lib/i18n.svelte";
  import { fmt, type Ui, type Status } from "../../lib/genui";
  import UiChart from "./UiChart.svelte";
  import UiTable from "./UiTable.svelte";
  import UiChoices from "./UiChoices.svelte";
  import UiForm from "./UiForm.svelte";
  let { items, keep }: { items: Ui[]; keep: Record<string, unknown> } = $props();

  // Each component's own state, kept apart.
  const own = (i: number) => (keep[i] ??= {}) as Record<string, unknown>;
  const show = (v: string | number) => typeof v === "number" ? fmt(v) : v;
  // A change up or down: its sign says which (a number's, or the text's first character).
  const trend = (d: string | number) => {
    const s = typeof d === "number" ? Math.sign(d) : /^\s*[+↑▲]/.test(d) ? 1 : /^\s*[-−↓▼]/.test(d) ? -1 : 0;
    return s > 0 ? "up" : s < 0 ? "down" : "";
  };
  const delta = (d: string | number) => typeof d === "number" ? (d > 0 ? "+" : "") + fmt(d) : d;
  const statusName = (s: Status) => ({ done: t("Done"), doing: t("In progress"), todo: t("To do"), failed: t("Failed") })[s];
</script>

<div class="gu">
  {#each items as c, i}
    <section class="card {c.type}">
      {#if c.title}<h4>{c.title}</h4>{/if}
      {#if c.type === "stats"}
        <div class="tiles">
          {#each c.items as s}
            <div class="tile">
              <div class="label">{s.label}</div>
              <div class="value">{show(s.value)}</div>
              {#if s.delta !== undefined}<div class="delta {trend(s.delta)}">{delta(s.delta)}</div>{/if}
            </div>
          {/each}
        </div>
      {:else if c.type === "chart"}
        <UiChart {c} />
      {:else if c.type === "table"}
        <UiTable {c} keep={own(i)} />
      {:else if c.type === "timeline"}
        <ol class="tl">
          {#each c.items as e}
            <li class={e.status ?? ""}>
              <span class="dot {e.status === 'doing' ? 'running' : e.status === 'failed' ? 'failed' : e.status ?? ''}"></span>
              <div><span class="time">{e.time}</span><div class="t">{e.title}</div>{#if e.text}<div class="txt">{e.text}</div>{/if}</div>
            </li>
          {/each}
        </ol>
      {:else if c.type === "kanban"}
        <div class="kb">
          {#each c.columns as col}
            <div class="col">
              <div class="ct">{col.title}<span class="muted">{col.cards.length}</span></div>
              {#each col.cards as k}
                <div class="kc"><div class="t">{k.title}</div>{#if k.text}<div class="txt">{k.text}</div>{/if}{#if k.tag}<span class="chip">{k.tag}</span>{/if}</div>
              {/each}
            </div>
          {/each}
        </div>
      {:else if c.type === "steps"}
        <ol class="steps">
          {#each c.items as s}
            <li class={s.status} title={statusName(s.status)}>
              <span class="mark" aria-label={statusName(s.status)}>{s.status === "done" ? "✓" : s.status === "failed" ? "✕" : ""}</span>
              <div><div class="t">{s.title}</div>{#if s.text}<div class="txt">{s.text}</div>{/if}</div>
            </li>
          {/each}
        </ol>
      {:else if c.type === "choices"}
        <UiChoices {c} keep={own(i)} />
      {:else if c.type === "form"}
        <UiForm {c} keep={own(i)} />
      {/if}
    </section>
  {/each}
</div>

<style>
  /* Never wider than the chat's column (its grids are minmax(0, 1fr)): a wide table or board
     scrolls inside its own card. */
  .gu { display: flex; flex-direction: column; gap: 10px; margin: 0 0 .8em; line-height: 1.45; overflow-wrap: anywhere; min-width: 0; max-width: 100%; }
  .card { border: 1px solid var(--line-soft); border-radius: 14px; padding: 12px 14px; background: var(--page); min-width: 0; max-width: 100%; overflow-x: auto; box-sizing: border-box; }
  h4 { margin: 0 0 9px; font-size: 13px; font-weight: 600; }
  .t { font-weight: 500; }
  .txt { color: var(--ink-2); font-size: 12.5px; white-space: pre-wrap; }

  .tiles { display: grid; grid-template-columns: repeat(auto-fill, minmax(130px, 1fr)); gap: 8px; }
  .tile { border: 1px solid var(--line-soft); border-radius: 10px; padding: 8px 11px; background: var(--card); }
  .tile .label { font-size: 11.5px; color: var(--muted); }
  .tile .value { font-size: 19px; font-weight: 600; font-variant-numeric: tabular-nums; }
  .delta { font-size: 12px; color: var(--muted); font-variant-numeric: tabular-nums; }
  .delta.up { color: var(--good-text); }
  .delta.down { color: var(--crit); }

  .gu .tl { list-style: none; margin: 0; padding: 0; }
  .gu .tl li { display: flex; gap: 11px; position: relative; padding: 0 0 11px; }
  .tl li:last-child { padding-bottom: 0; }
  .tl li:not(:last-child)::before { content: ""; position: absolute; left: 3px; top: 12px; bottom: 0; width: 1px; background: var(--line); }
  .tl .dot { margin-top: 6px; }
  .tl .time { font-size: 11.5px; color: var(--muted); font-variant-numeric: tabular-nums; }

  .kb { display: flex; gap: 8px; overflow-x: auto; align-items: flex-start; }
  .col { flex: 1 0 160px; max-width: 260px; background: var(--sel); border-radius: 10px; padding: 7px; display: flex; flex-direction: column; gap: 6px; }
  .ct { display: flex; justify-content: space-between; font-size: 12px; font-weight: 600; padding: 1px 3px; }
  .kc { background: var(--card); border: 1px solid var(--line-soft); border-radius: 8px; padding: 6px 9px; font-size: 12.5px; display: flex; flex-direction: column; gap: 3px; align-items: flex-start; }

  .gu .steps { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 7px; }
  .gu .steps li { display: flex; gap: 10px; align-items: flex-start; }
  .steps .mark { width: 16px; height: 16px; border-radius: 50%; border: 1.5px solid var(--line); flex: none; display: grid; place-items: center; font-size: 10px; margin-top: 2px; box-sizing: border-box; color: #fff; }
  .steps .done .mark { background: var(--good); border-color: var(--good); }
  .steps .doing .mark { border-color: var(--accent); box-shadow: inset 0 0 0 3px var(--page), inset 0 0 0 8px var(--accent); }
  .steps .failed .mark { background: var(--crit); border-color: var(--crit); }
  .steps .done .t { color: var(--ink-2); }
</style>
