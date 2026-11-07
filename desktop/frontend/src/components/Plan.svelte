<script lang="ts">
  import Icon from "./Icon.svelte";
  let { entries }: { entries: { status: string; content: string }[] } = $props();
  const done = $derived(entries.filter(e => e.status === "completed").length);
</script>

<div class="plan">
  <div class="h"><span>Plan</span><span class="muted num">{done}/{entries.length}</span><span class="bar"><span style="width:{entries.length ? (done / entries.length) * 100 : 0}%"></span></span></div>
  {#each entries as e}
    <div class="e" class:done={e.status === "completed"} class:now={e.status === "in_progress"}>
      <span class="st">{#if e.status === "completed"}<Icon name="check" size={12} />{:else if e.status === "in_progress"}<span class="spin"></span>{:else}<span class="ring"></span>{/if}</span>
      <span>{e.content}</span>
    </div>
  {/each}
</div>

<style>
  .plan { border: 1px solid var(--line-soft); border-radius: var(--r-row); padding: 10px 12px; margin: 6px 0; background: var(--card); display: grid; gap: 4px; }
  .h { display: flex; align-items: center; gap: 10px; font-weight: 600; font-size: 13px; margin-bottom: 2px; }
  .bar { flex: 1; height: 4px; border-radius: 4px; background: var(--track); overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--good); border-radius: 4px; transition: width .3s; }
  .e { display: flex; gap: 9px; align-items: flex-start; font-size: 13px; color: var(--ink-2); }
  .e.done { color: var(--muted); text-decoration: line-through; text-decoration-color: var(--line); }
  .e.now { color: var(--ink); font-weight: 500; }
  .st { width: 14px; display: grid; place-items: center; padding-top: 3px; color: var(--good); }
  .ring { width: 10px; height: 10px; border-radius: 50%; border: 1.5px solid var(--line); }
</style>
