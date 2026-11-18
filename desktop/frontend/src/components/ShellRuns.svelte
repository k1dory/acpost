<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import Icon from "./Icon.svelte";

  // What the person ran from the composer ("!git status") in this chat and the agent was not told
  // of yet: one still running (it can be stopped), one being sent, one that did not go by itself
  // (stopped, not run, not sent: it can be sent, or removed). Sent, it is a message of the chat.
  const runs = $derived(app.shellsHere());
  const secs = (ms: number) => (ms < 1000 ? `${ms} ms` : `${(ms / 1000).toFixed(1)} s`);
</script>

{#each runs as r (r.id)}
  <div class="sh card">
    <div class="h">
      <span class="mono cmd selectable"><span class="bang">!</span>{r.cmd}</span>
      <span class="sp"></span>
      {#if r.sending}<span class="chip">{t("sending")}</span>
      {:else if r.running}<span class="chip">{t("running")}</span><button class="btn ghost sm" onclick={() => app.stopShell(r.id)}>{t("Stop")}</button>
      {:else}
        {#if r.error}<span class="chip crit">{t("failed")}</span>
        {:else if r.stopped}<span class="chip">{t("stopped")}</span>
        {:else if r.timedOut}<span class="chip warn">{t("ran too long")}</span>
        {:else}<span class="chip" class:crit={r.code !== 0} title={t("Exit code")}>{r.code}</span>{/if}
        <span class="muted num small took">{secs(r.ms)}</span>
        {#if !r.error}<button class="btn ghost sm quote" onclick={() => app.sendShell(r.id)} title={t("Send the command and its output to the agent")} aria-label={t("Send to the agent")}><Icon name="send" size={12} /><span class="lbl">{t("Send to the agent")}</span></button>{/if}
        <button class="btn ghost icon sm" onclick={() => app.dropShell(r.id)} title={t("Remove")} aria-label={t("Remove")}><Icon name="x" size={11} /></button>
      {/if}
    </div>
    {#if r.error}<pre class="out err selectable">{r.error}</pre>
    {:else if r.output}<pre class="out selectable">{r.output}</pre>{#if r.cut}<div class="muted small more">{t("Some of the output is not kept")}</div>{/if}
    {:else if !r.running}<div class="muted small more">{t("No output")}</div>{/if}
  </div>
{/each}

<style>
  /* The card is as narrow as its column (the chat beside the IDE): the command gives way, what is
     to its right keeps its one line, and the button is its icon alone where the words do not fit. */
  .sh { padding: 4px 12px 6px; margin: 10px 0; border-radius: var(--r-row); border-color: var(--line-soft); box-shadow: none; container-type: inline-size; }
  .h { display: flex; align-items: center; gap: 8px; min-height: 26px; }
  .h > :not(.cmd):not(.sp) { flex: none; white-space: nowrap; }
  .cmd { font-size: 12px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; flex: 0 1 auto; }
  .quote { gap: 5px; }
  .quote :global(svg) { display: none; }
  @container (max-width: 430px) {
    .quote { padding: 0 6px; }
    .quote .lbl { display: none; }
    .quote :global(svg) { display: block; }
  }
  .bang { color: var(--accent); font-weight: 700; margin-right: 2px; }
  .sp { flex: 1 0 0; }
  .small { font-size: 11px; }
  .out { margin: 4px 0 4px; padding: 8px 10px; max-height: 320px; overflow: auto; border-radius: 8px; background: var(--page); font: 12px/1.45 var(--mono); white-space: pre; }
  .out.err { color: var(--crit); white-space: pre-wrap; word-break: break-word; }
  .more { padding: 4px 2px 0; }
</style>
