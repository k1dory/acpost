<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { tokens, money } from "../lib/format";
  import type { Usage } from "../lib/turn";
  let { u }: { u: Usage } = $props();
  const pct = $derived(u.context_size ? Math.min(100, ((u.context_used ?? 0) / u.context_size) * 100) : 0);
</script>

<div class="meter">
  {#if u.context_size}
    <span class="num">{tokens(u.context_used ?? 0)} / {tokens(u.context_size)} {t("tokens")}</span>
    <span class="bar" class:hi={pct > 80}><span style="width:{pct}%"></span></span>
  {/if}
  {#if u.input || u.output}
    <span class="num muted">{t("in {n}", { n: tokens(u.input ?? 0) })} · {t("out {n}", { n: tokens(u.output ?? 0) })}{u.cache_read ? " · " + t("cache {n}", { n: tokens(u.cache_read) }) : ""}</span>
  {/if}
  {#if u.cost !== undefined && u.cost !== null}<span class="num muted">{money(u.cost, u.currency)}</span>{/if}
</div>

<style>
  .meter { display: flex; align-items: center; gap: 10px; font-size: 12px; color: var(--ink-2); flex-wrap: wrap; }
  .bar { width: 90px; height: 4px; border-radius: 4px; background: var(--track); overflow: hidden; }
  .bar span { display: block; height: 100%; background: var(--accent); }
  .bar.hi span { background: var(--warn); }
</style>
