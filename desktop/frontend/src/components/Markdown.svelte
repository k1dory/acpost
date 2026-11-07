<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { Marked } from "marked";
  import DOMPurify from "dompurify";
  import hljs from "highlight.js/lib/common";
  import { Clipboard } from "@wailsio/runtime";
  let { text }: { text: string } = $props();

  const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
  // A code block: its language (the fence's, else guessed from the code), highlighted, with a copy
  // button in its header.
  const md = new Marked({ gfm: true, breaks: true });
  md.use({
    renderer: {
      code({ text: code, lang }) {
        const want = (lang ?? "").trim().split(/\s+/)[0].toLowerCase();
        let out = "", name = want;
        if (want && hljs.getLanguage(want)) out = hljs.highlight(code, { language: want, ignoreIllegals: true }).value;
        else if (!want && code.length < 20000) {
          const r = hljs.highlightAuto(code);
          if (r.language && r.relevance >= 5) { out = r.value; name = r.language; }
        }
        if (!out) out = esc(code);
        const label = name ? esc(hljs.getLanguage(name)?.name ?? name) : "text";
        return `<div class="code"><div class="ch"><span class="lang">${label}</span><button class="cp" type="button">${t("Copy")}</button></div>` +
          `<pre><code class="hljs">${out}</code></pre></div>`;
      },
    },
  });
  const html = $derived(DOMPurify.sanitize(md.parse(text ?? "", { async: false }) as string));

  async function copy(e: MouseEvent) {
    const btn = (e.target as HTMLElement).closest(".cp") as HTMLButtonElement | null;
    if (!btn) return;
    const code = btn.closest(".code")?.querySelector("code")?.textContent ?? "";
    // The page's clipboard first (no runtime needed), else the app's; neither may hang the button.
    const within = (p: Promise<unknown>) => Promise.race([p, new Promise((_, no) => setTimeout(() => no(new Error("timeout")), 1500))]);
    let ok = false;
    try { await within(navigator.clipboard.writeText(code)); ok = true; } catch {}
    if (!ok) try { await within(Clipboard.SetText(code)); ok = true; } catch {}
    btn.textContent = ok ? t("Copied") : t("Copy failed");
    setTimeout(() => (btn.textContent = t("Copy")), 1400);
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="md selectable" onclick={copy}>{@html html}</div>

<style>
  .md { line-height: 1.65; overflow-wrap: anywhere; font-size: 13.5px; min-width: 0; max-width: 100%; }
  .md :global(table) { display: block; overflow-x: auto; max-width: 100%; }
  .md :global(p) { margin: 0 0 .8em; }
  .md :global(p:last-child) { margin-bottom: 0; }
  .md :global(h1), .md :global(h2), .md :global(h3) { font-size: 14.5px; font-weight: 600; margin: 1em 0 .4em; }
  .md :global(ul), .md :global(ol) { margin: 0 0 .7em; padding-left: 1.3em; }
  .md :global(li) { margin: .15em 0; }
  .md :global(code) { font-family: var(--mono); font-size: 12px; background: var(--sel); padding: 1px 5px; border-radius: 6px; font-variant-ligatures: none; }
  .md :global(.code) { border: 1px solid var(--line-soft); border-radius: 14px; margin: 0 0 .8em; overflow: hidden; background: var(--page); }
  .md :global(.ch) { display: flex; align-items: center; justify-content: space-between; padding: 5px 8px 5px 14px; border-bottom: 1px solid var(--line-soft); background: var(--sel); }
  .md :global(.lang) { font: 500 11px var(--mono); color: var(--muted); text-transform: lowercase; }
  .md :global(.cp) { border: 0; background: transparent; color: var(--muted); font-size: 11.5px; padding: 3px 8px; border-radius: 7px; cursor: default; }
  .md :global(.cp:hover) { background: var(--hover); color: var(--ink); }
  .md :global(pre) { padding: 12px 14px; overflow: auto; margin: 0; }
  .md :global(pre code) { background: none; padding: 0; font-size: 12px; line-height: 1.55; overflow-wrap: normal; white-space: pre; }
  .md :global(a) { color: var(--accent); text-decoration: none; }
  .md :global(table) { border-collapse: collapse; margin: 0 0 .8em; font-size: 13px; }
  .md :global(th), .md :global(td) { border: 1px solid var(--line); padding: 5px 9px; }
  .md :global(blockquote) { margin: 0 0 .7em; padding-left: 12px; border-left: 3px solid var(--line); color: var(--ink-2); }

  /* Highlighting: few colours, readable in both themes. */
  .md :global(.hljs-keyword), .md :global(.hljs-selector-tag), .md :global(.hljs-built_in), .md :global(.hljs-type) { color: var(--hl-kw); }
  .md :global(.hljs-string), .md :global(.hljs-regexp), .md :global(.hljs-addition), .md :global(.hljs-attribute) { color: var(--hl-str); }
  .md :global(.hljs-number), .md :global(.hljs-literal), .md :global(.hljs-symbol) { color: var(--hl-num); }
  .md :global(.hljs-comment), .md :global(.hljs-quote), .md :global(.hljs-meta) { color: var(--muted); font-style: italic; }
  .md :global(.hljs-title), .md :global(.hljs-section), .md :global(.hljs-name) { color: var(--hl-fn); }
  .md :global(.hljs-variable), .md :global(.hljs-template-variable), .md :global(.hljs-params), .md :global(.hljs-attr) { color: var(--hl-var); }
  .md :global(.hljs-deletion) { color: var(--crit); }
  .md { --hl-kw: #a626a4; --hl-str: #50a14f; --hl-num: #b76b01; --hl-fn: #4078f2; --hl-var: #c45a2c; }
  :global([data-theme="dark"]) .md { --hl-kw: #c792ea; --hl-str: #a5d6a7; --hl-num: #f7c37a; --hl-fn: #82aaff; --hl-var: #f2a37a; }
</style>
