<script lang="ts">
  import hljs from "highlight.js/lib/common";
  import { t } from "../lib/i18n.svelte";
  import { app, type ArtifactRef } from "../lib/state.svelte";
  import { Node, errText, unb64 } from "../lib/api";
  import Icon from "./Icon.svelte";
  import Markdown from "./Markdown.svelte";
  import { pageSandbox } from "../lib/sanitize";
  import { bytes } from "../lib/names";

  // Artifacts: the files the agents handed back in this chat (attached to their replies) and the
  // files of this Mac a reply linked to — each shown as what it is: a page, a picture, a PDF,
  // Markdown, code; or opened in its own app, or saved.
  type Art = { name: string; type: string; size: number; path?: string; data: string };

  const attached = $derived(app.msgs.flatMap(m => (m.files ?? []).filter(f => f.reply).map(f => ({ msg: m.id, name: f.name }) as ArtifactRef)));
  const all = $derived.by(() => {
    const out = [...attached];
    for (const a of app.openedArtifacts) if (!out.some(x => x.msg === a.msg && x.name === a.name && x.path === a.path && x.base === a.base)) out.push(a);
    return out;
  });
  const cur = $derived(app.artifact ?? all[0] ?? null);

  let art = $state<Art | null>(null);
  let err = $state("");
  let loading = $state(false);
  let asked = 0;
  // A page's scripts run only once the person allows them, for that page (another shown: off again).
  let scripts = $state(false);
  $effect(() => {
    const a = cur;
    const my = ++asked;
    art = null; err = ""; scripts = false;
    if (!a) return;
    loading = true;
    (a.path ? Node.LocalFile(a.path, a.base ?? "") : Node.MessageFile(a.msg!, a.name))
      .then((r: any) => { if (my === asked) art = r; })
      .catch(e => { if (my === asked) err = errText(e); })
      .finally(() => { if (my === asked) loading = false; });
  });

  const kind = $derived.by(() => {
    if (!art) return "";
    const ty = art.type.split(";")[0].trim().toLowerCase();
    if (ty === "text/html") return "html";
    if (ty === "image/svg+xml" || ty.startsWith("image/")) return "image";
    if (ty === "application/pdf") return "pdf";
    if (ty === "text/markdown" || /\.(md|markdown)$/i.test(art.name)) return "markdown";
    if (ty.startsWith("text/") || /json|xml|yaml|javascript|x-sh/.test(ty)) return "text";
    return "other";
  });
  const text = $derived(art && (kind === "html" || kind === "markdown" || kind === "text") ? new TextDecoder().decode(unb64(art.data)) : "");
  const code = $derived.by(() => {
    if (kind !== "text" || !art) return "";
    const ext = art.name.split(".").pop()?.toLowerCase() ?? "";
    const plain = () => text.replace(/&/g, "&amp;").replace(/</g, "&lt;");
    if (text.length > 400000) return plain(); // big: all of it, unhighlighted
    try { return hljs.getLanguage(ext) ? hljs.highlight(text, { language: ext, ignoreIllegals: true }).value : hljs.highlightAuto(text).value; }
    catch { return plain(); }
  });
  // A PDF from a blob: (the page does not reach the network or the app).
  // Made when a PDF is shown, let go when it is not (another file, the panel gone).
  let pdf = $state("");
  $effect(() => {
    if (kind !== "pdf" || !art) { pdf = ""; return; }
    const url = URL.createObjectURL(new Blob([unb64(art.data) as BlobPart], { type: "application/pdf" }));
    pdf = url;
    return () => URL.revokeObjectURL(url);
  });
  const label = (a: ArtifactRef) => a.name || a.path || "";

  async function openOutside() {
    if (!art) return;
    try { if (cur?.path) await Node.OpenLocal(cur.path, cur.base ?? ""); else await Node.OpenArtifact(art.name, art.data); } catch (e) { err = errText(e); }
  }
  async function save() {
    if (!art) return;
    try { await Node.SaveArtifact(art.name, art.data); } catch (e) { err = errText(e); }
  }
</script>

<div class="af">
  {#if !all.length}
    <div class="empty">{t("Files the agents hand back, and files of this Mac a reply links to, show here.")}</div>
  {:else}
    <div class="list">
      {#each all as a (a.msg + "|" + a.name + "|" + (a.path ?? ""))}
        <button class="it" class:on={cur === a || (cur?.msg === a.msg && cur?.name === a.name && cur?.path === a.path)} onclick={() => (app.artifact = a)} title={a.path ?? a.name}>
          <Icon name="file" size={12} /><span>{label(a).split("/").pop()}</span>
        </button>
      {/each}
    </div>
    <div class="bar">
      <b class="nm" title={art?.path ?? art?.name}>{art?.name ?? cur?.name}</b>
      {#if art}<span class="muted small">{art.type.split(";")[0]} · {bytes(art.size)}</span>{/if}
      <span class="sp"></span>
      {#if kind === "html"}<button class="btn ghost sm" class:on={scripts} onclick={() => (scripts = !scripts)} title={t("The page's scripts run in a frame of their own; they reach nothing outside the app")}>{scripts ? t("Scripts on") : t("Allow scripts")}</button>{/if}
      <button class="btn ghost sm" disabled={!art} onclick={openOutside}>{t("Open")}</button>
      <button class="btn ghost sm" disabled={!art} onclick={save}>{t("Save as…")}</button>
    </div>
    <div class="view">
      {#if loading}<div class="empty"><span class="spin"></span></div>
      {:else if err}<div class="err selectable">{err}</div>
      {:else if art}
        {#if kind === "html"}
          <!-- A page by itself, in an origin of its own (no app, no node, no cookies); its scripts run
               only when allowed, and the app's policy (lib/csp.ts) keeps them from the network.
               The frame is made anew when that changes: a sandbox is read as a frame loads. -->
          {#key scripts}<iframe class="page" title={art.name} sandbox={pageSandbox(scripts)} srcdoc={text}></iframe>{/key}
        {:else if kind === "image"}
          <div class="img"><img alt={art.name} src={`data:${art.type.split(";")[0]};base64,${art.data}`} /></div>
        {:else if kind === "pdf"}
          <iframe class="page" title={art.name} src={pdf}></iframe>
        {:else if kind === "markdown"}
          <div class="md-wrap"><Markdown {text} /></div>
        {:else if kind === "text"}
          <pre class="code selectable"><code class="hljs">{@html code}</code></pre>
        {:else}
          <div class="empty">{t("This file is not shown here: open it in its own app, or save it.")}</div>
        {/if}
      {/if}
    </div>
  {/if}
</div>

<style>
  .af { height: 100%; display: flex; flex-direction: column; min-height: 0; }
  .list { display: flex; gap: 4px; flex-wrap: wrap; padding: 8px; border-bottom: 1px solid var(--line-soft); max-height: 96px; overflow: auto; }
  .it { display: inline-flex; align-items: center; gap: 5px; border: 1px solid var(--line-soft); background: transparent; border-radius: 8px; padding: 3px 8px; font-size: 12px; color: var(--ink-2); max-width: 220px; }
  .it span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .it.on { background: var(--sel); color: var(--ink); border-color: transparent; }
  .bar { display: flex; align-items: center; gap: 8px; padding: 6px 10px; border-bottom: 1px solid var(--line-soft); min-width: 0; }
  .nm { font-size: 12.5px; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; min-width: 0; }
  .small { font-size: 11px; white-space: nowrap; }
  .sp { flex: 1; }
  .view { flex: 1; min-height: 0; overflow: auto; display: flex; flex-direction: column; }
  .page { flex: 1; width: 100%; border: 0; background: #fff; }
  .img { padding: 12px; display: grid; place-items: center; }
  .img img { max-width: 100%; height: auto; border-radius: 8px; }
  .md-wrap { padding: 14px 18px; }
  .code { margin: 0; padding: 12px 14px; font: 11.5px/1.55 var(--mono); white-space: pre; }
  .code :global(.hljs-keyword), .code :global(.hljs-built_in), .code :global(.hljs-type) { color: var(--hl-kw); }
  .code :global(.hljs-string), .code :global(.hljs-attribute) { color: var(--hl-str); }
  .code :global(.hljs-number), .code :global(.hljs-literal) { color: var(--hl-num); }
  .code :global(.hljs-comment), .code :global(.hljs-meta) { color: var(--muted); font-style: italic; }
  .code :global(.hljs-title), .code :global(.hljs-name) { color: var(--hl-fn); }
  .code :global(.hljs-variable), .code :global(.hljs-params), .code :global(.hljs-attr) { color: var(--hl-var); }
  .af { --hl-kw: #a626a4; --hl-str: #50a14f; --hl-num: #b76b01; --hl-fn: #4078f2; --hl-var: #c45a2c; }
  :global([data-theme="dark"]) .af { --hl-kw: #c792ea; --hl-str: #a5d6a7; --hl-num: #f7c37a; --hl-fn: #82aaff; --hl-var: #f2a37a; }
  .err { margin: 10px; padding: 8px 10px; border-radius: 10px; background: var(--crit-bg); color: var(--crit); font-size: 12px; white-space: pre-line; }
</style>
