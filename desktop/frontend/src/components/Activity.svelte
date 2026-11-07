<script lang="ts">
  import { app, final } from "../lib/state.svelte";
  import { intent, toolName, toolState } from "../lib/turn";
  import { who, dur, clock } from "../lib/format";
  import Avatar from "./Avatar.svelte";

  const rows = $derived((app.rev, app.msgs.filter(m => m.to.startsWith("agent:")).map(m => {
    const t = app.turns[m.id];
    const tools = t ? [...t.tools.values()] : [];
    const asks = t ? t.waiting().length : 0;
    return { m, tools, asks, state: asks ? "ask" : m.state };
  })));
</script>

<div class="act scroll">
  {#each rows as r (r.m.id)}
    <div class="grp" class:sel={app.focus === r.m.id}>
      <button class="gh" onclick={() => (app.focus = r.m.id)}>
        <Avatar addr={r.m.to} size={20} />
        <span class="nm">{who(r.m.to).name}</span>
        <span class="what muted">{(r.m.body ?? "").split("\n")[0]}</span>
        {#if r.state === "ask"}<span class="chip ask">Needs approval</span>
        {:else if r.state === "running"}<span class="chip"><span class="spin"></span> Running</span>
        {:else if r.state === "queued"}<span class="chip warn">Queued</span>
        {:else if r.state === "failed"}<span class="chip crit">Failed</span>
        {:else}<span class="chip">Done</span>{/if}
      </button>
      {#each r.tools as t (t.id)}
        {@const s = toolState(t, final(r.m.state))}
        <div class="tl" class:failed={s === "failed"}>
          <span class="dot {s === 'running' ? 'running' : s === 'failed' ? 'failed' : s === 'pending' ? 'queued' : 'done'}"></span>
          <span class="tn mono">{toolName(t)}</span>
          <span class="ti">{intent(t)}</span>
          <span class="muted num tt">{t.ended ? dur(t.ended - t.started) : clock(new Date(t.started).toISOString())}</span>
        </div>
      {/each}
    </div>
  {:else}
    <div class="empty">What the agents do shows up here.</div>
  {/each}
</div>

<style>
  .act { height: 100%; padding: 8px; }
  .grp { border-radius: var(--r-row); padding: 4px; margin-bottom: 6px; }
  .grp.sel { background: var(--hover); }
  .gh { width: 100%; display: flex; align-items: center; gap: 8px; border: 0; background: transparent; padding: 6px; text-align: left; border-radius: 10px; }
  .nm { font-weight: 600; flex: none; }
  .what { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-size: 13px; }
  .tl { display: flex; align-items: center; gap: 9px; padding: 5px 8px 5px 14px; font-size: 13px; border-left: 1px solid var(--line); margin-left: 15px; }
  .tl.failed { background: var(--crit-bg); color: var(--crit); border-radius: 0 10px 10px 0; }
  .tn { color: var(--muted); font-size: 11.5px; min-width: 40px; }
  .ti { flex: 1; min-width: 0; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .tt { font-size: 11.5px; }
</style>
