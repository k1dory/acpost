<script lang="ts">
  import { marked } from "marked";
  import DOMPurify from "dompurify";
  let { text }: { text: string } = $props();
  const html = $derived(DOMPurify.sanitize(marked.parse(text ?? "", { async: false, gfm: true, breaks: false }) as string));
</script>

<div class="md selectable">{@html html}</div>

<style>
  .md { line-height: 1.6; overflow-wrap: anywhere; }
  .md :global(p) { margin: 0 0 .7em; }
  .md :global(p:last-child) { margin-bottom: 0; }
  .md :global(h1), .md :global(h2), .md :global(h3) { font-size: 15.5px; font-weight: 600; margin: 1em 0 .4em; }
  .md :global(ul), .md :global(ol) { margin: 0 0 .7em; padding-left: 1.3em; }
  .md :global(li) { margin: .15em 0; }
  .md :global(code) { font-family: var(--mono); font-size: 12.5px; background: var(--sel); padding: 1px 5px; border-radius: 6px; }
  .md :global(pre) { background: var(--page); border: 1px solid var(--line-soft); border-radius: 14px; padding: 12px 14px; overflow: auto; margin: 0 0 .8em; }
  .md :global(pre code) { background: none; padding: 0; font-size: 12.5px; line-height: 1.5; }
  .md :global(a) { color: var(--accent); text-decoration: none; }
  .md :global(table) { border-collapse: collapse; margin: 0 0 .8em; font-size: 13px; }
  .md :global(th), .md :global(td) { border: 1px solid var(--line); padding: 5px 9px; }
  .md :global(blockquote) { margin: 0 0 .7em; padding-left: 12px; border-left: 3px solid var(--line); color: var(--ink-2); }
</style>
