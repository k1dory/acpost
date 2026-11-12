<script lang="ts">
  import { untrack } from "svelte";
  import { t } from "../lib/i18n.svelte";
  import { Node, errText, on } from "../lib/api";
  import { dur, clock, who } from "../lib/format";
  import { analytics } from "../lib/analytics.svelte";
  import { Turn, type Ev } from "../lib/turn";
  import { EventFeed } from "../lib/feed";
  import { marksOf, traceTurn, rows, shown, ticks, type TThread, type Row } from "../lib/trace";
  import PhaseBar from "./PhaseBar.svelte";

  // A turn's timeline: each call, permission wait and message to another agent on the turn's time
  // axis, under what ran it; the phases on top; what a span carried beside it. Its events are read
  // whole (the node's list has only their timing); a running turn's come as the chat's stream
  // brings them (the window gets them whoever follows the message) and are asked for every two
  // seconds besides.
  let { thread, id, now, reread = 0, onchange }: { thread: TThread; id: string; now: number; reread?: number; onchange: () => void } = $props();

  const msg = $derived(thread.messages.find(m => m.id === id));
  // The shown turn's events (lib/feed.ts): one feed a turn, stopped when another is shown, so
  // nothing asked for one turn lands in another's.
  let feed = $state.raw<EventFeed | null>(null);
  let full = new Turn();
  let rev = $state(0);
  let loading = $state(true);
  let readErr = $state(""); // the last read of its events failed

  $effect(() => {
    const at = id;
    const f = untrack(() => {
      const tn = new Turn();
      full = tn; rev++; readErr = ""; loading = true; sel = "";
      const f: EventFeed = new EventFeed(after => Node.Events(at, after) as any, fresh => { tn.add(fresh); if (feed === f) rev++; });
      feed = f;
      return f;
    });
    drain(f, 1).then(() => { if (feed === f) loading = false; });
    const off = on("events:" + at, (b: any) => {
      if (b?.events?.length) f.live(b.events as Ev[]);
      if (b?.final) onchange();
    });
    return () => { off(); f.stop(); };
  });

  const marks = $derived.by(() => { rev; return feed ? marksOf(feed.raw) : []; });
  const trace = $derived(msg ? traceTurn(msg, marks, thread.messages, now) : null);
  const running = $derived(!!trace?.running);
  // While the turn runs its events are asked for every two seconds; once it is over, they are read
  // to their end (the turn's state may come before its last events).
  $effect(() => {
    const f = feed;
    if (!f || !running) return;
    const poll = setInterval(() => { f.sync().catch(() => {}); }, 2000); // the next poll is the retry
    return () => { clearInterval(poll); drain(f); };
  });

  // Every read to the end goes here — the first, the last once the turn is over (a few times if it
  // fails: no poll comes after), Refresh and Retry: one that fails is shown, one that works clears it.
  async function drain(f: EventFeed, tries = 3) {
    for (let i = 0; ; i++) {
      try {
        await f.sync();
        if (feed === f) readErr = "";
        return;
      } catch (e) {
        if (feed !== f) return;
        if (i + 1 >= tries) { readErr = errText(e); return; }
        await new Promise(r => setTimeout(r, 500 * (i + 1)));
      }
    }
  }
  $effect(() => {
    if (!reread) return;
    const f = untrack(() => feed);
    if (f) drain(f, 1);
  });

  // Filter, zoom.
  let text = $state("");
  let hideShort = $state(false);
  let zoom = $state(1);
  let sel = $state("");
  const shownRows = $derived(trace ? rows(trace.spans, { text, hideShort }) : []);
  const selected = $derived(trace?.spans.find(s => s.id === sel));

  const labelW = 300;
  let cw = $state(800);
  let scroller = $state<HTMLDivElement>();
  const len = $derived(trace ? Math.max(1, trace.end - trace.start) : 1);
  const trackW = $derived(Math.max(200, (cw - labelW - 2) * zoom));
  const x = (at: number) => ((at - (trace?.start ?? 0)) / len) * trackW;
  const axis = $derived(ticks(len, Math.max(3, Math.round(trackW / 110))));
  // ⌘ or ctrl and the wheel zoom on the time under the pointer.
  $effect(() => {
    const el = scroller;
    if (!el) return;
    const wheel = (e: WheelEvent) => {
      if (!e.metaKey && !e.ctrlKey) return;
      e.preventDefault();
      const r = el.getBoundingClientRect();
      const px = Math.max(0, e.clientX - r.left - labelW);
      const at = (el.scrollLeft + px) / trackW;
      const z = Math.min(400, Math.max(1, zoom * (e.deltaY < 0 ? 1.25 : 0.8)));
      if (z === zoom) return;
      zoom = z;
      queueMicrotask(() => { el.scrollLeft = at * Math.max(200, (cw - labelW - 2) * z) - px; });
    };
    el.addEventListener("wheel", wheel, { passive: false });
    return () => el.removeEventListener("wheel", wheel);
  });

  const tool = $derived.by(() => { rev; return selected?.kind === "tool" ? full.tools.get(selected.ref) : undefined; });
  const request = $derived.by(() => { rev; return selected?.kind === "permission" ? full.requests.get(selected.ref) : undefined; });
  const child = $derived(selected?.kind === "agent" ? thread.messages.find(m => m.id === selected.ref) : undefined);
  const parentTurn = $derived(msg?.parent ? thread.messages.find(m => m.id === msg.parent) : undefined);
  const stateNames: Record<string, string> = { running: "running", done: "done", failed: "failed", cancelled: "cancelled", unknown: "unknown", waiting: "waiting" };
  const kindNames: Record<string, string> = { tool: "Call", permission: "Approval", agent: "Agent" };
</script>

{#if !msg}
  <div class="empty muted">{t("This turn is not in the conversation's last turns")}</div>
{:else if trace}
  <div class="top">
    <div class="who"><b class="mono">{who(msg.to).name}</b>
      {#if trace.running}<span class="chip warn">{t("running")}</span>{:else if msg.state !== "done"}<span class="chip" class:crit={msg.state === "failed"}>{t(msg.state)}</span>{/if}
      <span class="muted num">{clock(msg.created_at)}</span>
      {#if parentTurn}<button class="btn sm" onclick={() => (analytics.turn = parentTurn.id)}>{t("The turn that asked")}</button>{/if}
    </div>
    <div class="stats num">
      <span><span class="muted">{t("Length")}</span> {dur(trace.end - trace.start)}</span>
      <span><span class="muted">{t("Active")}</span> {dur(trace.active) || "—"}</span>
      <span><span class="muted">{t("Calls")}</span> {trace.calls}</span>
      <span class:bad={trace.errors > 0}><span class="muted">{t("Errors")}</span> {trace.errors}</span>
    </div>
  </div>
  {#if trace.truncated}<div class="note warn">{t("The node kept no more of this turn's events: calls after that are missing, and its sums are short")}</div>{/if}
  <div class="card ph"><PhaseBar phases={trace.phases} legend height={10} /></div>

  <div class="tools">
    <input class="flt" placeholder={t("Filter by name")} bind:value={text} />
    <label class="muted chk"><input type="checkbox" bind:checked={hideShort} /> {t("Hide under 1 s")}</label>
    <span class="sp"></span>
    <span class="muted hint">{t("⌘ or ctrl and scroll to zoom")}</span>
    {#if zoom > 1}<button class="btn sm" onclick={() => (zoom = 1)}>{t("Fit")}</button>{/if}
  </div>

  {#if readErr}<div class="note warn rd">{t("This turn's events could not be read: what is shown may be short")} — {readErr}
    <button class="btn sm" onclick={() => feed && drain(feed, 1)}>{t("Retry")}</button></div>{/if}
  <div class="body" class:with={!!selected}>
    <div class="card tl scroll" bind:this={scroller} bind:clientWidth={cw}>
      <div class="inner" style="width:{labelW + trackW}px">
        <div class="axis" style="grid-template-columns:{labelW}px {trackW}px">
          <div class="lab muted">{loading ? t("Loading…") : t("{n} spans", { n: shownRows.length })}</div>
          <div class="trk">{#each axis as a}<span class="tick num" style="left:{(a / len) * trackW}px">{dur(a) || "0"}</span>{/each}</div>
        </div>
        {#if trace.first !== null && trace.first > trace.start}
          <div class="row q" style="grid-template-columns:{labelW}px {trackW}px">
            <div class="lab muted">{t("Waiting to start")}</div>
            <div class="trk"><span class="bar queue" style="left:0;width:{Math.max(2, x(trace.first))}px"></span></div>
          </div>
        {/if}
        {#each shownRows as r (r.id)}
          {@render row(r)}
        {:else}
          {#if !loading}<div class="none muted">{text || hideShort ? t("Nothing matches the filter") : t("No calls in this turn")}</div>{/if}
        {/each}
      </div>
    </div>

    {#if selected}
      <aside class="card det">
        <div class="dh"><span class="chip">{t(kindNames[selected.kind])}</span><span class="sp"></span><button class="btn ghost sm" onclick={() => (sel = "")}>{t("Close")}</button></div>
        <div class="dt selectable">{selected.label}</div>
        <div class="kv num">
          <span class="muted">{t("Started")}</span><span>+{dur(selected.start - trace.start) || "0"}</span>
          <span class="muted">{t("Duration")}</span><span>{dur(selected.end - selected.start) || "0"}</span>
          <span class="muted">{t("State")}</span><span class:bad={selected.state === "failed"}>{t(stateNames[selected.state])}</span>
          {#if selected.kind === "tool"}<span class="muted">{t("Kind")}</span><span class="mono">{selected.name}</span>{/if}
          {#if typeof selected.exit === "number"}<span class="muted">{t("Exit code")}</span><span>{selected.exit}</span>{/if}
        </div>
        {#if tool}
          {#if tool.description}<div class="sec"><div class="k muted">{t("Description")}</div><pre class="selectable">{shown(tool.description, 2000)}</pre></div>{/if}
          {#if shown(tool.input)}<div class="sec"><div class="k muted">{t("Input")}</div><pre class="selectable">{shown(tool.input)}</pre></div>{/if}
          {#if tool.locations?.length}<div class="sec"><div class="k muted">{t("Files")}</div><pre class="selectable">{shown(tool.locations.join("\n"), 4000)}</pre></div>{/if}
          {#if shown(tool.output)}<div class="sec"><div class="k muted" class:bad={selected.state === "failed"}>{selected.state === "failed" ? t("Error") : t("Output")}</div><pre class="selectable">{shown(tool.output)}</pre></div>{/if}
        {:else if request}
          {#if request.message || request.text}<div class="sec"><pre class="selectable">{shown(request.message || request.text, 4000)}</pre></div>{/if}
          {#if shown(request.raw_input)}<div class="sec"><div class="k muted">{t("Input")}</div><pre class="selectable">{shown(request.raw_input)}</pre></div>{/if}
          <div class="kv"><span class="muted">{t("Decision")}</span><span>{request.decision ?? t("waiting")}{request.by ? ` · ${request.by}` : ""}</span></div>
        {:else if child}
          <div class="kv"><span class="muted">{t("Agent")}</span><span class="mono">{child.to}</span>
            {#if child.error_code}<span class="muted">{t("Error")}</span><span class="bad mono">{child.error_code}</span>{/if}</div>
          <button class="btn sm" onclick={() => (analytics.turn = child.id)}>{t("Open its turn")}</button>
        {:else if selected.kind === "tool"}
          <div class="muted small">{loading ? t("Loading…") : t("Its events are not here")}</div>
        {/if}
      </aside>
    {/if}
  </div>
  {#if msg.cut}<div class="note muted">{t("Not all of this turn's events are here")}</div>{/if}
{/if}

{#snippet row(r: Row)}
  <button class="row" class:on={sel === r.id} style="grid-template-columns:{labelW}px {trackW}px" onclick={() => (sel = sel === r.id ? "" : r.id)}
    ondblclick={() => { if (r.kind === "agent") analytics.turn = r.ref; }}>
    <div class="lab" style="padding-left:{10 + r.depth * 16}px" title={r.label}>
      <span class="dot {r.kind} {r.state}"></span><span class="nm mono">{r.kind === "permission" ? t("approval") : r.name}</span><span class="lb">{r.label === r.name ? "" : r.label}</span>
    </div>
    <div class="trk">
      <span class="bar {r.kind} {r.state}" style="left:{x(r.start)}px;width:{Math.max(2, x(r.end) - x(r.start))}px"></span>
      <span class="d num muted" style="left:{x(r.end) + 6}px">{dur(r.end - r.start)}</span>
    </div>
  </button>
{/snippet}

<style>
  .top { display: flex; align-items: center; gap: 16px; flex-wrap: wrap; margin-bottom: 10px; }
  .who { display: flex; align-items: center; gap: 8px; font-size: 13px; }
  .stats { display: flex; gap: 16px; font-size: 12.5px; margin-left: auto; }
  .ph { padding: 14px 16px; margin-bottom: 12px; }
  .tools { display: flex; align-items: center; gap: 10px; margin-bottom: 10px; font-size: 12px; }
  .flt { height: 28px; border: 1px solid var(--line); border-radius: 9px; padding: 0 10px; background: var(--card); color: var(--ink); font: inherit; width: 220px; }
  .chk { display: inline-flex; align-items: center; gap: 5px; }
  .sp { flex: 1; }
  .hint { font-size: 11.5px; }
  .body { display: grid; grid-template-columns: minmax(0, 1fr); gap: 12px; align-items: start; }
  .body.with { grid-template-columns: minmax(0, 1fr) 360px; }
  .tl { overflow: auto; max-height: calc(100vh - 300px); padding: 0; }
  .inner { position: relative; }
  .axis, .row { display: grid; align-items: center; }
  .axis { position: sticky; top: 0; z-index: 2; background: var(--card); border-bottom: 1px solid var(--line-soft); height: 30px; }
  .lab { position: sticky; left: 0; z-index: 1; background: var(--card); height: 100%; display: flex; align-items: center; gap: 7px; padding-right: 10px; overflow: hidden; white-space: nowrap; border-right: 1px solid var(--line-soft); font-size: 12px; min-width: 0; }
  .axis .lab { padding-left: 10px; }
  .trk { position: relative; height: 100%; }
  .tick { position: absolute; top: 8px; font-size: 10.5px; color: var(--muted); padding-left: 3px; border-left: 1px solid var(--line-soft); height: 22px; }
  .row { width: 100%; height: 28px; border: 0; padding: 0; background: transparent; text-align: left; font: inherit; color: inherit; cursor: pointer; border-bottom: 1px solid var(--line-soft); }
  .row:hover .lab, .row:hover { background: var(--hover); }
  .row.on .lab, .row.on { background: var(--sel); }
  .row.q { cursor: default; }
  .nm { font-weight: 500; flex: none; }
  .lb { overflow: hidden; text-overflow: ellipsis; color: var(--ink-2); min-width: 0; }
  .dot { width: 7px; height: 7px; border-radius: 50%; flex: none; background: var(--good); }
  .bar { position: absolute; top: 8px; height: 12px; border-radius: 4px; background: var(--good); }
  .d { position: absolute; top: 7px; font-size: 10.5px; white-space: nowrap; }
  .dot.permission, .bar.permission { background: var(--warn); }
  .dot.agent, .bar.agent { background: color-mix(in srgb, var(--accent) 45%, var(--crit)); }
  .bar.queue { background: color-mix(in srgb, var(--muted) 55%, var(--track)); }
  .dot.failed, .bar.failed { background: var(--crit); }
  .bar.running, .bar.waiting { background-image: repeating-linear-gradient(45deg, transparent 0 5px, rgba(255,255,255,.35) 5px 10px); }
  .dot.cancelled, .bar.cancelled, .dot.unknown, .bar.unknown { opacity: .5; }
  .none { padding: 18px; text-align: center; font-size: 12.5px; }
  .det { padding: 14px 16px; display: grid; gap: 10px; max-height: calc(100vh - 300px); overflow: auto; font-size: 12.5px; }
  .dh { display: flex; align-items: center; }
  .dt { font-weight: 600; word-break: break-word; }
  .kv { display: grid; grid-template-columns: auto 1fr; gap: 4px 12px; }
  .sec .k { font-size: 11.5px; margin-bottom: 4px; }
  pre { margin: 0; white-space: pre-wrap; word-break: break-word; font-family: var(--mono); font-size: 11.5px; background: var(--page); border: 1px solid var(--line-soft); border-radius: 8px; padding: 8px 10px; max-height: 280px; overflow: auto; }
  .bad { color: var(--crit); }
  .small { font-size: 12px; }
  .note { font-size: 12px; padding: 10px 6px; }
  .note.warn { color: var(--warn); }
</style>
