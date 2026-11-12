<script lang="ts" module>
  import type { Phase } from "../lib/trace";
  // A phase as a person reads it.
  export const phaseNames: Record<Phase, string> = {
    queue: "Waiting to start", model: "Model", tools: "Tools", approval: "Waiting for approval", agents: "Other agents",
  };
</script>

<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { dur } from "../lib/format";
  import { phaseOrder, type Phases } from "../lib/trace";

  // Where a turn's (or a conversation's) time went, as one bar; with the legend under it.
  let { phases, legend = false, height = 8 }: { phases: Phases; legend?: boolean; height?: number } = $props();
  const total = $derived(phaseOrder.reduce((a, p) => a + phases[p], 0));
</script>

<div class="pb" style="height:{height}px" title={phaseOrder.filter(p => phases[p] > 0).map(p => `${t(phaseNames[p])}: ${dur(phases[p])}`).join("\n")}>
  {#if total > 0}
    {#each phaseOrder as p}{#if phases[p] > 0}<span class="ph {p}" style="width:{(phases[p] / total) * 100}%"></span>{/if}{/each}
  {/if}
</div>
{#if legend}
  <div class="lg muted">
    {#each phaseOrder as p}<span class="it"><span class="sw ph {p}"></span>{t(phaseNames[p])} <span class="num">{dur(phases[p]) || "0ms"}</span>{#if total > 0}<span class="pc num">{Math.round((phases[p] / total) * 100)}%</span>{/if}</span>{/each}
  </div>
{/if}

<style>
  .pb { display: flex; width: 100%; border-radius: 8px; background: var(--track); overflow: hidden; }
  .ph.queue { background: color-mix(in srgb, var(--muted) 55%, var(--track)); }
  .ph.model { background: var(--accent); }
  .ph.tools { background: var(--good); }
  .ph.approval { background: var(--warn); }
  .ph.agents { background: color-mix(in srgb, var(--accent) 45%, var(--crit)); }
  .lg { display: flex; flex-wrap: wrap; gap: 4px 14px; font-size: 12px; margin-top: 8px; }
  .it { display: inline-flex; align-items: center; gap: 6px; }
  .sw { width: 10px; height: 10px; border-radius: 3px; display: inline-block; }
  .pc { opacity: .7; }
</style>
