<script lang="ts">
  import { t } from "../lib/i18n.svelte";
  import { Marked } from "marked";
  import { cleanHtml, cleanSvg } from "../lib/sanitize";
  import hljs from "highlight.js/lib/common";
  import { Clipboard } from "@wailsio/runtime";
  import { mount, unmount, untrack } from "svelte";
  import { parseUi, keepFor, fenceClosed } from "../lib/genui";
  import GenUi from "./genui/GenUi.svelte";
  // scope: what names the place of this text (a message, a turn of a history), for what a person did
  // to its components to be kept when it is shown again; without one it is kept while it is shown.
  let { text, scope = "" }: { text: string; scope?: string } = $props();

  const esc = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
  // A code block: its language (the fence's, else guessed from the code), highlighted, with a copy
  // button in its header.
  const md = new Marked({ gfm: true, breaks: true });
  // A file path written as code (`src/app.ts`, `/tmp/report.html`): clickable, opens as an artifact.
  const filePath = /^(~\/|\/|\.{1,2}\/)?[\w@.+-]+(\/[\w@.+-]+)*\.[A-Za-z0-9]{1,8}$/;
  md.use({
    renderer: {
      // A link to a file of this Mac: an artifact (a file: href would not survive the sanitizer, and
      // must not navigate).
      link(token) {
        if (!/^file:/i.test(token.href)) return false;
        let path = "";
        try {
          const u = new URL(token.href);
          if (u.host && u.host !== "localhost") return this.parser.parseInline(token.tokens); // another machine's: text
          path = decodeURIComponent(u.pathname);
        } catch { return this.parser.parseInline(token.tokens); }
        return `<a href="#artifact:${encodeURIComponent(path)}">${this.parser.parseInline(token.tokens)}</a>`;
      },
      codespan({ text: code }) {
        const raw = code; // as written: entities in code stay literal
        return filePath.test(raw) && raw.includes("/") || /\.(html?|md|png|jpe?g|gif|svg|pdf|csv|json)$/i.test(raw) && filePath.test(raw)
          ? `<code class="pl" title="${esc(raw)}">${esc(raw)}</code>` : `<code>${esc(raw)}</code>`;
      },
      code({ text: code, lang, raw }) {
        const want = (lang ?? "").trim().split(/\s+/)[0].toLowerCase();
        // A diagram: drawn after (Mermaid, loaded when first needed), its text kept to show if it fails.
        if (want === "mermaid") return `<div class="mm"><pre><code>${esc(code)}</code></pre></div>`;
        // Components (```ui): drawn after, like a diagram; until the block is whole (its closing fence
        // come) and valid, its code.
        const ui = want === "ui" && fenceClosed(raw);
        let out = "", name = ui ? "json" : want;
        if (name && hljs.getLanguage(name)) out = hljs.highlight(code, { language: name, ignoreIllegals: true }).value;
        else if (!want && code.length < 20000) {
          const r = hljs.highlightAuto(code);
          if (r.language && r.relevance >= 5) { out = r.value; name = r.language; }
        }
        if (!out) out = esc(code);
        const label = want === "ui" ? "ui" : name ? esc(hljs.getLanguage(name)?.name ?? name) : "text";
        return (ui ? `<div class="uib">` : "") + `<div class="code"><div class="ch"><span class="lang">${label}</span><span class="cp" role="button" tabindex="0" title="${t("Copy")}" aria-label="${t("Copy")}">` +
          `<svg class="i-copy" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.4" stroke-linecap="round" stroke-linejoin="round"><rect x="5.5" y="5.5" width="8" height="8" rx="1.6"/><path d="M10.5 5.5V3.6c0-.6-.5-1.1-1.1-1.1H3.6c-.6 0-1.1.5-1.1 1.1v5.8c0 .6.5 1.1 1.1 1.1h1.9"/></svg>` +
          `<svg class="i-ok" viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M3.5 8.5l3 3 6-7"/></svg></span></div>` +
          `<pre><code class="hljs">${out}</code></pre></div>` + (ui ? `</div>` : "");
      },
    },
  });
  // No forms or fields, nothing loaded from the web as it is shown (lib/sanitize.ts).
  const html = $derived(cleanHtml(md.parse(text ?? "", { async: false }) as string));

  let box: HTMLDivElement;
  let seq = 0;

  // The page is the HTML's, put in node by node: a top-level node the new HTML has as it was stays
  // where it is (a reply streaming in changes its end), and with it what is drawn in it (a diagram,
  // components and what is typed in them, the focus).
  const madeOf = new WeakMap<Node, string>();
  const sig = (n: Node) => n.nodeType === Node.ELEMENT_NODE ? (n as Element).outerHTML : n.nodeType + ":" + n.textContent;
  function patch(h: string) {
    const tpl = document.createElement("template");
    tpl.innerHTML = h; // sanitized (cleanHtml); a template's content is inert
    const fresh = [...tpl.content.childNodes], old = [...box.childNodes];
    fresh.forEach((f, i) => {
      const s = sig(f), o = old[i];
      if (o && madeOf.get(o) === s) return;
      madeOf.set(f, s);
      if (o) box.replaceChild(f, o); else box.append(f);
    });
    for (const o of old.slice(fresh.length)) o.remove();
  }

  // Components: each ui block whose text is valid drawn in its place, its code hidden (a block not
  // valid, or still being written, stays code). One drawn before, at the same place with the same
  // text, is the same: kept, moved into its block if the block is new, never mounted again.
  type Live = { inst: ReturnType<typeof mount>; host: HTMLElement };
  const live = new Map<string, Live>();
  const own = new Map<string, Record<string, unknown>>(); // without a scope: this text's, while shown
  function keep(n: number, src: string) {
    if (scope) return keepFor(scope, n, src);
    const key = n + "\u0000" + src;
    let k = own.get(key);
    if (!k) own.set(key, k = {});
    return k;
  }
  function drawUi() {
    const used = new Set<string>();
    [...box.querySelectorAll<HTMLElement>(".uib")].forEach((b, n) => {
      const src = b.querySelector("code")?.textContent ?? "";
      const key = n + "\u0000" + src;
      const have = live.get(key);
      if (have) {
        used.add(key);
        if (have.host.parentElement !== b) b.append(have.host);
        b.classList.add("drawn");
        return;
      }
      const p = parseUi(src);
      if (!p.ok) return;
      const host = document.createElement("div");
      b.append(host);
      b.classList.add("drawn");
      try {
        live.set(key, { inst: mount(GenUi, { target: host, props: { items: p.items, keep: keep(n, src) } }), host });
        used.add(key);
      } catch (e) { console.warn("ui block not drawn:", (e as Error)?.message?.slice(0, 200)); host.remove(); b.classList.remove("drawn"); }
    });
    for (const [k, l] of live) if (!used.has(k)) { unmount(l.inst); l.host.remove(); live.delete(k); }
  }

  // A field being typed in whose block was moved (its top-level node new) has the focus back, and
  // the selection in it.
  function focused() {
    const el = document.activeElement as HTMLInputElement | null;
    if (!el || !box.contains(el)) return null;
    let sel: [number, number, string] | null = null;
    try { if (el.selectionStart !== null && el.selectionStart !== undefined) sel = [el.selectionStart, el.selectionEnd ?? el.selectionStart, el.selectionDirection ?? "none"]; } catch {}
    return { el, sel };
  }
  function refocus(f: ReturnType<typeof focused>) {
    if (!f || !f.el.isConnected) return;
    f.el.focus({ preventScroll: true });
    try { if (f.sel) f.el.setSelectionRange(f.sel[0], f.sel[1], f.sel[2] as "forward" | "backward" | "none"); } catch {}
  }

  $effect(() => {
    const h = html;
    untrack(() => {
      const f = focused();
      patch(h);
      // Out of the page now: its block's node was new (a browser takes the focus off it).
      const moved = f && !f.el.isConnected;
      drawUi();
      if (moved) refocus(f);
    });
  });
  $effect(() => () => { for (const l of live.values()) unmount(l.inst); live.clear(); });
  // Diagrams: each drawn once its block is in the page; a drawing that fails leaves the text.
  $effect(() => {
    html;
    // The diagram's text is the block's own (an attribute would not survive the sanitizer).
    const blocks = [...box.querySelectorAll<HTMLElement>(".mm:not([data-done])")];
    if (!blocks.length) return;
    (async () => {
      let mermaid: typeof import("mermaid").default;
      try { mermaid = (await import("mermaid")).default; } catch (e) { console.warn("diagrams unavailable:", (e as Error)?.message); return; }
      mermaid.initialize({ startOnLoad: false, securityLevel: "strict", suppressErrorRendering: true, htmlLabels: false, flowchart: { htmlLabels: false },
        theme: document.documentElement.dataset.theme === "dark" ? "dark" : "default" });
      for (const b of blocks) {
        b.dataset.done = "1";
        try {
          const src = b.querySelector("code")?.textContent ?? "";
          const id = "mm-" + Date.now().toString(36) + "-" + seq++;
          let svg = "";
          try { svg = (await mermaid.render(id, src)).svg; } finally { document.getElementById("d" + id)?.remove(); document.getElementById(id)?.remove(); }
          b.innerHTML = cleanSvg(svg);
          b.classList.add("drawn");
        } catch (e) { console.warn("diagram not drawn:", (e as Error)?.message?.slice(0, 200)); /* the text stays */ }
      }
    })();
  });

  // Copy from the keyboard too (Enter or Space on it).
  function copyKey(e: KeyboardEvent) {
    if ((e.key === "Enter" || e.key === " ") && (e.target as HTMLElement).closest(".cp")) { e.preventDefault(); copy(e); }
  }
  async function copy(e: Event) {
    const btn = (e.target as HTMLElement).closest(".cp") as HTMLButtonElement | null;
    if (!btn) return;
    const code = btn.closest(".code")?.querySelector("code")?.textContent ?? "";
    // The page's clipboard first (no runtime needed), else the app's; neither may hang the button.
    const within = (p: Promise<unknown>) => Promise.race([p, new Promise((_, no) => setTimeout(() => no(new Error("timeout")), 1500))]);
    let ok = false;
    try { await within(navigator.clipboard.writeText(code)); ok = true; } catch {}
    if (!ok) try { await within(Clipboard.SetText(code)); ok = true; } catch {}
    // The icon says it: a check while copied (the title says it in words).
    btn.classList.toggle("done", ok);
    btn.title = ok ? t("Copied") : t("Copy failed");
    setTimeout(() => { btn.classList.remove("done"); btn.title = t("Copy"); }, 1400);
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_static_element_interactions -->
<div class="md selectable" bind:this={box} onclick={copy} onkeydown={copyKey}></div>

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
  .md :global(.cp) { display: inline-grid; place-items: center; width: 26px; height: 24px; border: 0; background: transparent; color: var(--muted); border-radius: 7px; cursor: default; }
  .md :global(.cp .i-ok) { display: none; }
  .md :global(.cp.done .i-copy) { display: none; }
  .md :global(.cp.done .i-ok) { display: block; color: var(--good); }
  .md :global(.cp:hover) { background: var(--hover); color: var(--ink); }
  .md :global(pre) { padding: 12px 14px; overflow: auto; margin: 0; }
  .md :global(pre code) { background: none; padding: 0; font-size: 12px; line-height: 1.55; overflow-wrap: normal; white-space: pre; }
  .md :global(.mm) { margin: 0 0 .8em; }
  .md :global(.mm.drawn) { border: 1px solid var(--line-soft); border-radius: 14px; padding: 14px; background: var(--page); overflow: auto; text-align: center; }
  .md :global(.mm.drawn svg) { max-width: 100%; height: auto; }
  .md :global(.mm pre) { border: 1px solid var(--line-soft); border-radius: 14px; }
  .md :global(.uib.drawn > .code) { display: none; }
  .md :global(code.pl) { cursor: pointer; text-decoration: underline dotted; text-underline-offset: 3px; }
  .md :global(code.pl:hover) { color: var(--accent); }
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
