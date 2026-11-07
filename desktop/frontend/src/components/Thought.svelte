<script lang="ts">
  import { dur } from "../lib/format";
  import Icon from "./Icon.svelte";
  let { text, ms, live }: { text: string; ms: number; live: boolean } = $props();
  let open = $state(false);
</script>

<div class="th">
  <button class="head" onclick={() => (open = !open)}>
    <Icon name="brain" size={14} />
    <span>{live ? "Thinking…" : ms >= 1000 ? `Thought for ${dur(ms)}` : "Thought briefly"}</span>
    <span class="chev" class:rot={open}><Icon name="chev" size={12} /></span>
  </button>
  {#if open}<div class="body selectable">{text}</div>{/if}
</div>

<style>
  .th { margin: 4px 0; }
  .head { display: inline-flex; align-items: center; gap: 7px; border: 0; background: transparent; color: var(--muted); padding: 4px 6px; border-radius: 10px; font-size: 13px; }
  .head:hover { background: var(--hover); color: var(--ink-2); }
  .chev { display: grid; transition: transform .15s; } .chev.rot { transform: rotate(90deg); }
  .body { margin: 4px 0 6px 6px; padding-left: 12px; border-left: 2px solid var(--line); color: var(--ink-2); font-size: 13px; white-space: pre-wrap; max-height: 300px; overflow: auto; }
</style>
