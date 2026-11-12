<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import Icon from "./Icon.svelte";

  // What the person ran from the composer ("!git status") in this chat: the command, what it
  // printed, how it ended. Theirs alone: the agent sees it only if they put it in a message.
  const runs = $derived(app.shellsHere());
  const secs = (ms: number) => (ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`);
</script>

{#each runs as r (r.id)}
  <div class="sh card">
    <div class="h">
      <span class="mono cmd selectable"><span class="bang">!</span>{r.cmd}</span>
      <span class="sp"></span>
      {#if r.running}<span class="chip">{t("running")}</span><button class="btn ghost sm" onclick={() => app.stopShell(r.id)}>{t("Stop")}</button>
      {:else}
        {#if r.error}<span class="chip crit">{t("failed")}</span>
        {:else if r.stopped}<span class="chip">{t("stopped")}</span>
        {:else if r.timedOut}<span class="chip warn">{t("ran too long")}</span>
        {:else}<span class="chip" class:crit={r.code !== 0} title={t("Exit code")}>{r.code}</span>{/if}
        <span class="muted num small">{secs(r.ms)}</span>
        <button class="btn ghost sm" onclick={() => app.quoteShell(r.id)} title={t("Put the command and its output into the message")}>{t("Add to message")}</button>
        <button class="btn ghost icon sm" onclick={() => app.dropShell(r.id)} title={t("Remove")} aria-label={t("Remove")}><Icon name="x" size={11} /></button>
      {/if}
    </div>
    {#if r.error}<pre class="out err selectable">{r.error}</pre>
    {:else if r.output}<pre class="out selectable">{r.output}</pre>{#if r.cut}<div class="muted small more">{t("The rest of the output is not kept")}</div>{/if}
    {:else if !r.running}<div class="muted small more">{t("No output")}</div>{/if}
  </div>
{/each}

<style>
  .sh { padding: 8px 10px; margin: 10px 0; border-radius: var(--r-panel); }
  .h { display: flex; align-items: center; gap: 8px; min-height: 26px; }
  .cmd { font-size: 12.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
  .bang { color: var(--accent); font-weight: 700; margin-right: 2px; }
  .sp { flex: 1; }
  .small { font-size: 11px; }
  .out { margin: 6px 0 0; padding: 8px 10px; max-height: 320px; overflow: auto; border-radius: 8px; background: var(--hover); font: 12px/1.45 var(--mono); white-space: pre-wrap; word-break: break-word; }
  .out.err { color: var(--crit); }
  .more { padding: 4px 2px 0; }
</style>
