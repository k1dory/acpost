<script lang="ts">
  // A few fields from a reply: the person fills them, and the answers go into their message being
  // written. Not an HTML form: nothing here submits anywhere.
  import { untrack } from "svelte";
  import { t } from "../../lib/i18n.svelte";
  import { formAnswer, type Form, type Field } from "../../lib/genui";
  import { toDraft } from "./draft";
  let { c, keep }: { c: Form; keep: Record<string, unknown> } = $props();

  const start = (f: Field) => f.value ?? (f.type === "checkbox" ? false : f.type === "select" ? f.options![0] : "");
  // As it was left: a mounted block's props do not change (Markdown.svelte mounts it anew).
  let values = $state<(string | number | boolean)[]>(untrack(() => (keep.values as (string | number | boolean)[] | undefined) ?? c.fields.map(start)));
  let added = $state(untrack(() => !!keep.added));
  const id = "f" + Math.random().toString(36).slice(2, 8);

  function set(i: number, v: string | number | boolean) {
    values[i] = v;
    keep.values = $state.snapshot(values);
    added = keep.added = false;
  }
  // In place of the answers added before, if the draft still has them.
  function answer() {
    const text = formAnswer(c, values, t("yes"), t("no"));
    toDraft(text, (keep.sent as string | undefined) ?? "");
    keep.sent = text;
    added = keep.added = true;
  }
</script>

<div class="form">
  {#each c.fields as f, i}
    <div class="row" class:check={f.type === "checkbox"}>
      {#if f.type === "checkbox"}
        <label><input type="checkbox" checked={!!values[i]} onchange={e => set(i, (e.target as HTMLInputElement).checked)} />{f.label}</label>
      {:else}
        <label for="{id}-{i}">{f.label}</label>
        {#if f.type === "select"}
          <select id="{id}-{i}" class="input" value={values[i]} onchange={e => set(i, (e.target as HTMLSelectElement).value)}>
            {#each f.options ?? [] as o}<option value={o}>{o}</option>{/each}
          </select>
        {:else}
          <input id="{id}-{i}" class="input" type={f.type === "number" ? "number" : "text"} value={values[i]} placeholder={f.placeholder ?? ""}
            oninput={e => { const v = (e.target as HTMLInputElement).value; set(i, f.type === "number" && v !== "" && Number.isFinite(+v) ? +v : v); }} />
        {/if}
      {/if}
    </div>
  {/each}
  <div class="foot">
    <button type="button" class="btn primary" disabled={added} onclick={answer}>{c.submit ?? t("Add to message")}</button>
    {#if added}<span class="muted">{t("Added to your message")}</span>{/if}
  </div>
</div>

<style>
  .form { display: flex; flex-direction: column; gap: 9px; max-width: 420px; }
  .row { display: flex; flex-direction: column; gap: 4px; }
  .row > label { font-size: 12px; color: var(--ink-2); font-weight: 500; }
  .row.check > label { display: flex; align-items: center; gap: 8px; font-size: 13px; color: var(--ink); font-weight: 400; }
  .row .input { height: 28px; font-size: 13px; padding: 0 9px; }
  .foot { display: flex; align-items: center; gap: 10px; margin-top: 2px; font-size: 12px; }
</style>
