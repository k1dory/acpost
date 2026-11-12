<script lang="ts">
  import { initials, hue, who } from "../lib/format";
  import AgentMark, { markOf } from "./AgentMark.svelte";
  let { addr, size = 22, busy = false, brand = "" }: { addr: string; size?: number; busy?: boolean; brand?: string } = $props();
  const w = $derived(who(addr));
  const h = $derived(hue(w.name));
  // An agent whose CLI is known (by what the node says runs behind it, its name, or the brand given:
  // a cloud agent's) shows its mark.
  const mark = $derived(w.kind !== "user" ? markOf(brand || addr) : "");
</script>

{#if mark}
  <span title={addr}><AgentMark of={brand || addr} {size} {busy} /></span>
{:else}
  <span class="av" class:user={w.kind === "user"} style="--h:{h};width:{size}px;height:{size}px;font-size:{Math.round(size * 0.42)}px" title={addr}>
    {initials(w.name)}
  </span>
{/if}

<style>
  .av {
    display: inline-grid; place-items: center; border-radius: 8px; flex: none; font-weight: 600;
    color: hsl(var(--h) 45% 30%); background: hsl(var(--h) 60% 93%);
    box-shadow: inset 0 1px 0 rgba(255,255,255,.7), 0 0 0 1px hsl(var(--h) 30% 85%);
  }
  :global([data-theme="dark"]) .av { color: hsl(var(--h) 60% 80%); background: hsl(var(--h) 30% 20%); box-shadow: 0 0 0 1px hsl(var(--h) 25% 28%); }
  .av.user { color: var(--btn-ink); background: var(--btn); box-shadow: none; }
  span[title] { display: inline-grid; }
</style>
