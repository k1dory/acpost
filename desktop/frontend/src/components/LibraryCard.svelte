<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { Node, unb64 } from "../lib/api";
  import { ago } from "../lib/format";
  import { agentLabel } from "../lib/names";
  import { inView } from "../lib/visible";
  import { cardKind, firstLines, typeLabel, webLink, type LibItem } from "../lib/library";
  import Avatar from "./Avatar.svelte";
  import Icon from "./Icon.svelte";

  // A card of the library: a little of the file (asked for once the card comes near the screen,
  // never before), its name, its chat, its agent, how long ago.
  let { item, onopen, onchat }: { item: LibItem; onopen: (a: LibItem) => void; onchat: (a: LibItem) => void } = $props();

  const kind = $derived(cardKind(item));
  let shown = $state<{ type: string; data: string } | null>(null);
  let asked = false;
  function near(v: boolean) {
    if (!v || asked || !kind) return;
    asked = true;
    Node.ArtifactPreview(item.kind === "file" ? item.msg : "", item.name, item.kind === "file" ? "" : item.path ?? "")
      .then((r: any) => { if (r?.data) shown = { type: (r.type ?? "").split(";")[0], data: r.data }; })
      .catch(() => {}); // the card keeps its icon
  }
  const text = $derived(shown && kind === "text" ? firstLines(new TextDecoder().decode(unb64(shown.data))) : "");
  const where = $derived(webLink(item) || item.path || "");
</script>

<div class="card lc" class:gone={item.gone} use:inView={near}>
  <button class="hit" onclick={() => onopen(item)} title={where || item.name}>
    <div class="pv">
      {#if shown && kind === "image"}
        <img alt={item.name} src={`data:${shown.type};base64,${shown.data}`} />
      {:else if text}
        <pre class="tx">{text}</pre>
      {:else}
        <div class="ic"><Icon name={webLink(item) ? "link" : "file"} size={26} /><span class="ty mono">{typeLabel(item)}</span></div>
      {/if}
    </div>
    <div class="nm">{item.name}</div>
  </button>
  <div class="ft">
    {#if item.agent}<span class="ag"><Avatar addr={item.agent} size={14} /></span>{/if}
    {#if item.thread}
      <button class="chat" onclick={() => onchat(item)} title={t("Open the chat")}>{item.title || t("Untitled chat")}</button>
    {:else}<span class="chat muted">{agentLabel(item.agent)}</span>{/if}
    <span class="when muted">{item.gone ? t("no longer there") : ago(item.at)}</span>
  </div>
</div>

<style>
  .lc { display: flex; flex-direction: column; overflow: hidden; min-width: 0; padding: 0; }
  .lc.gone { opacity: .6; }
  .hit { display: flex; flex-direction: column; border: 0; background: transparent; padding: 0; text-align: left; color: inherit; min-width: 0; }
  .pv { height: 132px; background: var(--hover); display: grid; place-items: center; overflow: hidden; border-bottom: 1px solid var(--line-soft); }
  .pv img { max-width: 100%; max-height: 132px; object-fit: contain; }
  .tx { margin: 0; align-self: start; justify-self: stretch; padding: 10px 12px; font: 10.5px/1.45 var(--mono); color: var(--ink-2); white-space: pre-wrap; overflow: hidden; height: 132px; box-sizing: border-box; }
  .ic { display: grid; justify-items: center; gap: 6px; color: var(--muted); }
  .ty { font-size: 10.5px; letter-spacing: .04em; }
  .nm { padding: 8px 12px 2px; font-size: 12.5px; font-weight: 500; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ft { display: flex; align-items: center; gap: 6px; padding: 2px 12px 10px; font-size: 11.5px; min-width: 0; }
  .ag { display: inline-grid; flex: none; }
  .chat { flex: 1; min-width: 0; border: 0; background: transparent; padding: 0; text-align: left; color: var(--ink-2); font-size: 11.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  button.chat:hover { color: var(--ink); text-decoration: underline; }
  .when { flex: none; white-space: nowrap; }
</style>
