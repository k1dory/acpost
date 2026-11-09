<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import type { Request } from "../lib/turn";
  import Icon from "./Icon.svelte";

  let { req, msg }: { req: Request; msg: string } = $props();
  let show = $state(false);
  let busy = $state(false);
  const allow = $derived(req.options?.find(o => o.kind.startsWith("allow")) ?? req.options?.[0]);
  const reject = $derived(req.options?.find(o => o.kind.startsWith("reject")));
  const raw = $derived(req.raw_input ? (typeof req.raw_input === "string" ? req.raw_input : JSON.stringify(req.raw_input, null, 2)) : "");
  async function pick(option: string) {
    busy = true;
    await app.answer(msg, req.request_id, option);
    busy = false;
  }
</script>

<div class="card ask" class:done={!!req.decision}>
  <div class="row">
    <span class="ic"><Icon name="alert" /></span>
    <div class="t">
      <div class="title">{req.title || t("The agent asks")}</div>
      {#if req.message || req.text}<div class="desc muted selectable">{req.message || req.text}</div>{/if}
    </div>
    {#if req.decision}
      <span class="chip" class:good={req.decision.startsWith("allow") || req.decision === "selected"} class:crit={req.decision === "rejected" || req.decision === "timeout"}>{req.decision}{req.by ? ` · ${req.by.replace(/^user:/, "")}` : ""}</span>
    {:else}
      <span class="chip ask">{t("Needs approval")}</span>
    {/if}
  </div>
  {#if raw}
    <button class="btn ghost sm" onclick={() => (show = !show)}>{show ? t("Hide request") : t("Show request")}</button>
    {#if show}<pre class="raw selectable">{raw}</pre>{/if}
  {/if}
  {#if !req.decision}
    <div class="acts">
      {#if reject}<button class="btn" disabled={busy} onclick={() => pick(reject.option_id)}>{reject.name || t("Reject")}</button>
      {:else}<button class="btn" disabled={busy} onclick={() => pick("")}>{t("Reject")}</button>{/if}
      {#each (req.options ?? []).filter(o => o !== allow && o !== reject) as o}
        <button class="btn" disabled={busy} onclick={() => pick(o.option_id)}>{o.name}</button>
      {/each}
      {#if allow}<button class="btn primary" disabled={busy} onclick={() => pick(allow.option_id)}>{allow.name || t("Allow")}</button>{/if}
    </div>
  {/if}
</div>

<style>
  .ask { padding: 14px 16px; display: grid; gap: 10px; border-color: color-mix(in srgb, var(--warn) 35%, var(--line)); }
  .ask.done { border-color: var(--line); opacity: .85; }
  .row { display: flex; gap: 12px; align-items: flex-start; }
  .ic { color: var(--warn); padding-top: 2px; }
  .t { flex: 1; min-width: 0; }
  .title { font-weight: 600; }
  .desc { font-size: 12.5px; margin-top: 2px; white-space: pre-wrap; }
  .raw { margin: 0; font: 12px/1.5 var(--mono); background: var(--page); border: 1px solid var(--line-soft); border-radius: 12px; padding: 10px; max-height: 240px; overflow: auto; white-space: pre-wrap; }
  .acts { display: flex; gap: 8px; justify-content: flex-end; }
  .btn.ghost { justify-self: start; }
</style>
