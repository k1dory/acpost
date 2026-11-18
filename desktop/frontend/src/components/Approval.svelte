<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { app } from "../lib/state.svelte";
  import type { Request } from "../lib/turn";
  import Icon from "./Icon.svelte";

  let { req, msg }: { req: Request; msg: string } = $props();
  let show = $state(false);
  let busy = $state(false);
  let full = $state(false);
  const allow = $derived(req.options?.find(o => o.kind.startsWith("allow")) ?? req.options?.[0]);
  const reject = $derived(req.options?.find(o => o.kind.startsWith("reject")));
  // A command to run is shown as one: its lines kept (a heredoc), in the code font, a few of them.
  const isCmd = $derived(req.tool_kind === "execute" && !!req.title);
  const raw = $derived(req.raw_input ? (typeof req.raw_input === "string" ? req.raw_input : JSON.stringify(req.raw_input, null, 2)) : "");
  async function pick(option: string) {
    busy = true;
    await app.answer(msg, req.request_id, option);
    busy = false;
  }
</script>

<div class="card ask" class:done={!!req.decision} data-ask={req.decision ? undefined : ""}>
  <div class="row">
    <span class="ic"><Icon name="alert" /></span>
    <div class="t">
      {#if isCmd}
        <!-- Allow approves all of it: a command longer than its few lines unfolds whole. -->
        <button class="title cmd selectable" class:full onclick={() => (full = !full)} title={full ? t("Fold") : t("Show all of it")}>{req.title}</button>
      {:else}<div class="title">{req.title || t("The agent asks")}</div>{/if}
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
      {#if reject}<button class="btn" disabled={busy} onclick={() => pick(reject.option_id)}>{reject.name ? t(reject.name) : t("Reject")}</button>
      {:else}<button class="btn" disabled={busy} onclick={() => pick("")}>{t("Reject")}</button>{/if}
      {#each (req.options ?? []).filter(o => o !== allow && o !== reject) as o}
        <button class="btn" disabled={busy} onclick={() => pick(o.option_id)}>{t(o.name)}</button>
      {/each}
      {#if allow}<button class="btn primary" disabled={busy} onclick={() => pick(allow.option_id)}>{allow.name ? t(allow.name) : t("Allow")}</button>{/if}
    </div>
  {/if}
</div>

<style>
  /* In a narrow window: the title and its text wrap, the state goes under them, the choices wrap
     onto lines of their own and a long one breaks inside its button. */
  /* .card.ask, not .ask: the state chip is .chip.ask and must not be drawn as a second card. */
  .card.ask { padding: 14px 16px; display: grid; grid-template-columns: minmax(0, 1fr); gap: 10px; border-color: color-mix(in srgb, var(--warn) 35%, var(--line)); }
  /* One that waits holds the turn: it is the loudest thing in the chat until it is answered. */
  .card.ask:not(.done) { border-color: var(--warn); background: var(--warn-bg); box-shadow: 0 0 0 3px color-mix(in srgb, var(--warn) 14%, transparent); }
  .card.ask:not(.done)::before { display: none; }
  .card.ask.done { border-color: var(--line); box-shadow: none; }
  .card.ask.done .title { font-weight: 500; }
  .row { display: flex; gap: 8px 12px; align-items: flex-start; flex-wrap: wrap; }
  .row .chip { flex: none; margin-left: auto; }
  .ic { color: var(--warn); padding-top: 2px; }
  .card.ask.done .ic { color: var(--muted); }
  .t { flex: 1 1 180px; min-width: 0; }
  .title { font-weight: 600; overflow-wrap: anywhere; }
  .title.cmd { font: 500 12.5px/1.5 var(--mono); white-space: pre-wrap; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 6; line-clamp: 6; overflow: hidden; text-align: left; padding: 0; border: 0; background: none; color: inherit; cursor: pointer; width: 100%; }
  .title.cmd.full { display: block; -webkit-line-clamp: unset; line-clamp: unset; }
  .desc { font-size: 12.5px; margin-top: 2px; white-space: pre-wrap; overflow-wrap: anywhere; }
  .raw { margin: 0; font: 12px/1.5 var(--mono); background: var(--page); border: 1px solid var(--line-soft); border-radius: 12px; padding: 10px; max-height: 240px; overflow: auto; white-space: pre-wrap; }
  .acts { display: flex; gap: 8px; justify-content: flex-end; flex-wrap: wrap; }
  .acts .btn { max-width: 100%; min-width: 0; height: auto; min-height: 28px; padding: 3px 11px; justify-content: center; white-space: normal; text-align: center; overflow-wrap: anywhere; }
  .btn.ghost { justify-self: start; }
</style>
