<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { analytics, type AnalyticsTab } from "../lib/analytics.svelte";
  import Usage from "./Usage.svelte";
  import Traces from "./Traces.svelte";
  import Commands from "./Commands.svelte";

  // Analytics: what agents used and cost (Usage), where their time went turn by turn (Traces), and
  // what their calls ran (Commands). Each tab puts the title and the tabs in its own header.
  const tabs: [AnalyticsTab, string][] = [["usage", "Usage"], ["traces", "Traces"], ["commands", "Commands"]];
</script>

{#snippet head()}
  <h1 class="ah">{t("Analytics")}</h1>
  <div class="seg nodrag">{#each tabs as [k, l]}<button class:on={analytics.tab === k} onclick={() => (analytics.tab = k)}>{t(l)}</button>{/each}</div>
{/snippet}

{#if analytics.tab === "traces"}<Traces {head} />
{:else if analytics.tab === "commands"}<Commands {head} />
{:else}<Usage {head} />{/if}

<style>
  .ah { font-size: 16px; font-weight: 600; margin: 0 6px 0 0; }
</style>
