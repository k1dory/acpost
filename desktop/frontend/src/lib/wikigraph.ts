// What the agents keep, as one set: the pages of every repository's wiki and the user's knowledge
// notes, with the links between them. A body links to another item as [[target]] or
// [[target|label]]: a page's target is first a page of its own wiki (by slug), then a note (by
// name); a note's is a note, then a page of the wiki of its directory. A page is also part of its
// parent. A link to nothing is kept as broken, so it can be shown and fixed.

import { Marked, type MarkedExtension, type Token, type TokenizerAndRendererExtension } from "marked";

export type Kind = "page" | "note";
export type Item = {
  id: string;
  kind: Kind;
  title: string;
  /** A page's slug, a note's name: what a link names. */
  key: string;
  /** A page's repository (its wiki). */
  repo?: string;
  /** A note's directory (empty: every session). */
  scope?: string;
  parent?: string;
  body: string;
  updated: string;
  enabled?: boolean;
};
export type EdgeType = "link" | "part of";
export type Edge = { from: string; to: string; type: EdgeType };
export type Broken = { from: string; target: string };
export type Link = { target: string; label?: string };
export type Graph = {
  items: Item[];
  byId: Map<string, Item>;
  edges: Edge[];
  out: Map<string, Edge[]>;
  in: Map<string, Edge[]>;
  broken: Broken[];
  /** Items with no edge either way. */
  lonely: Item[];
  /** What a target resolves to from an item (null: nothing). */
  resolve: (from: Item, target: string) => Item | null;
};

export type PageIn = { repo: string; slug: string; title: string; parent?: string; body?: string; updated?: any };
export type NoteIn = { name: string; body?: string; scope?: string; enabled?: boolean; updated?: any };

export const pageId = (repo: string, slug: string) => "page:" + repo + "#" + slug;
export const noteId = (name: string) => "note:" + name;

/** A [[link]] as marked reads it: its target, and its label's own inline tokens (code, emphasis). */
export type WikiLinkToken = { type: "wikilink"; raw: string; target: string; label?: string; tokens: Token[] };
/** What a target resolves to, for the renderer: an item (its id and title), or null (broken). */
export type WikiResolve = (target: string) => { id: string; title: string } | null;

// The pipe between target and label may be escaped (\|): in a table row it must be (tableSafe).
const wikiRule = /^\[\[([^[\]|\n]{1,200}?)(?:\\?\|((?:[^[\]\n]|\[[^[\]\n]*\]){0,200}))?\]\]/;
const escHtml = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

/**
 * The wiki link as a marked inline extension: marked tokenizes it where it tokenizes text, so a
 * [[link]] in a code block, a code span or HTML is never one, and the graph (parseLinks) and the
 * page (Markdown with resolve) read a body the same way. The renderer makes an in-app link —
 * #wiki:<id> to an item, #wiki-broken:<target> for one that names nothing.
 */
export function wikiLinkExtension(resolve?: WikiResolve): TokenizerAndRendererExtension {
  return {
    name: "wikilink",
    level: "inline",
    start: (src: string) => src.indexOf("[["),
    tokenizer(src: string) {
      const m = wikiRule.exec(src);
      const target = m?.[1].trim();
      if (!m || !target) return undefined;
      const label = m[2]?.trim() || undefined;
      const tok: WikiLinkToken = { type: "wikilink", raw: m[0], target, label, tokens: label ? plainLabel(this.lexer.inlineTokens(label)) : [] };
      return tok;
    },
    renderer(token) {
      const tok = token as unknown as WikiLinkToken;
      const to = resolve?.(tok.target) ?? null;
      const inner = tok.label ? this.parser.parseInline(tok.tokens) : escHtml(to ? to.title : tok.target);
      const href = to ? "#wiki:" + encodeHref(to.id) : "#wiki-broken:" + encodeHref(tok.target);
      return `<a href="${href}">${inner}</a>`;
    },
  };
}

// What a label keeps of its Markdown: text, code spans, emphasis, strong, strike-through and
// escapes. Anything else — a link, an autolinked address or e-mail, a picture, HTML — is its text,
// at any depth: a label never holds a link of its own (the click is the wiki link's) nor markup.
const labelKeeps = new Set(["text", "codespan", "em", "strong", "del", "escape"]);
function plainLabel(tokens: Token[]): Token[] {
  return tokens.map(t => {
    const tok = t as Token & { tokens?: Token[]; text?: string };
    if (!labelKeeps.has(tok.type)) {
      const text = tok.type === "html" ? tok.raw : tok.text ?? tok.raw;
      return { type: "text", raw: tok.raw, text } as Token; // the text renderer escapes it
    }
    return tok.tokens ? ({ ...tok, tokens: plainLabel(tok.tokens) } as Token) : tok;
  });
}

/**
 * A body with the pipe of each labelled wiki link in a table row escaped ([[a\|b]]): GFM splits a
 * row into cells at every unescaped pipe before any inline syntax is read, so [[a|b]] would be cut
 * in two. The table rows are those marked finds (a table at any depth: in a quote, a list item);
 * the rest of the body is left as it is. Within a row, a code span is left alone too: GFM's rule
 * holds there — its pipe splits the cell unless the author escaped it.
 */
export function tableSafe(body: string): string {
  const text = body ?? "";
  if (!text.includes("[[") || !text.includes("|")) return text;
  // Tables are found in the body with every labelled link's pipe escaped (a header row with one
  // would otherwise have a column too many, and be no table); its lines are the body's, one for one.
  const probe = text.replace(labelled, "[[$1\\|$2]]");
  let tokens: Token[];
  try { tokens = reader.lexer(probe); } catch { return text; }
  // Each top-level token's raw is the next stretch of the text; a table's rows are lines of it
  // (behind a quote's ">" or a list item's indent when nested), found in order within it.
  const rowsAt = new Set<number>();
  let line = 0;
  for (const top of tokens) {
    const rows: string[] = [];
    reader.walkTokens([top], t => { if (t.type === "table") rows.push(...t.raw.split("\n").map(l => l.trim()).filter(Boolean)); });
    const lines = top.raw.split("\n");
    let k = 0;
    lines.forEach((l, i) => { if (k < rows.length && l.trimEnd().endsWith(rows[k])) { k++; rowsAt.add(line + i); } });
    line += lines.length - 1;
  }
  if (!rowsAt.size) return text;
  return text.split("\n").map((l, i) => (rowsAt.has(i) ? escapeRow(l) : l)).join("\n");
}
// A label may hold brackets one deep ([[a|see [x](y)]]), as the tokenizer reads it.
const labelled = /\[\[([^[\]|\n]*[^[\]|\n\\])\|((?:[^[\]\n]|\[[^[\]\n]*\]){0,200})\]\]/g;
// A row with its wiki links' pipes escaped, outside its code spans.
const escapeRow = (line: string) => line.split(/((`+)[^`]+?\2(?!`))/).map((part, i) => (i % 3 === 0 ? part.replace(labelled, "[[$1\\|$2]]") : part)).filter((_, i) => i % 3 !== 2).join("");

/** The wiki's Markdown for marked: the link, and the body made safe for tables first. */
export function wikiMarkdown(resolve?: WikiResolve): MarkedExtension {
  return { extensions: [wikiLinkExtension(resolve)], hooks: { preprocess: tableSafe } };
}

// The options the app renders Markdown with (components/Markdown.svelte): the graph lexes alike.
export const markdownOptions = { gfm: true, breaks: true } as const;
const reader = new Marked(markdownOptions).use({ extensions: [wikiLinkExtension()] });

/** The [[links]] of a body, in order, where Markdown has them (not in code or HTML). */
export function parseLinks(body: string): Link[] {
  const found: Link[] = [];
  let tokens: Token[];
  try { tokens = reader.lexer(tableSafe(body ?? "")); } catch { return found; } // text marked cannot read: no links
  reader.walkTokens(tokens, t => {
    if (t.type !== "wikilink") return;
    const w = t as unknown as WikiLinkToken;
    found.push(w.label ? { target: w.target, label: w.label } : { target: w.target });
  });
  return found;
}

/** A body as the app shows it, wiki links made links (resolve) — without the app's code blocks. */
export function renderWiki(body: string, resolve: WikiResolve): string {
  return new Marked(markdownOptions).use(wikiMarkdown(resolve)).parse(body ?? "", { async: false }) as string;
}

/** The set, its edges, backlinks, broken links and the items on their own. */
export function buildGraph(pages: PageIn[], notes: NoteIn[]): Graph {
  const items: Item[] = [];
  const byId = new Map<string, Item>();
  const wikis = new Map<string, Map<string, Item>>();
  const named = new Map<string, Item>();
  const namedLower = new Map<string, Item>();
  for (const p of pages) {
    const it: Item = { id: pageId(p.repo, p.slug), kind: "page", title: p.title || p.slug, key: p.slug, repo: p.repo, parent: p.parent || undefined, body: p.body ?? "", updated: String(p.updated ?? "") };
    if (byId.has(it.id)) continue;
    items.push(it); byId.set(it.id, it);
    let w = wikis.get(p.repo);
    if (!w) wikis.set(p.repo, (w = new Map()));
    w.set(p.slug, it);
  }
  for (const k of notes) {
    const it: Item = { id: noteId(k.name), kind: "note", title: k.name, key: k.name, scope: k.scope || "", body: k.body ?? "", updated: String(k.updated ?? ""), enabled: k.enabled };
    if (byId.has(it.id)) continue;
    items.push(it); byId.set(it.id, it);
    named.set(k.name, it);
    if (!namedLower.has(k.name.toLowerCase())) namedLower.set(k.name.toLowerCase(), it);
  }
  // A note's wiki: that of its directory, or of the closest repository it is in.
  const repos = [...wikis.keys()].sort((a, b) => b.length - a.length);
  const wikiOf = (dir: string) => repos.find(r => dir === r || dir.startsWith(r.endsWith("/") ? r : r + "/"));

  function resolve(from: Item, target: string): Item | null {
    const t = target.trim();
    const lower = t.toLowerCase();
    const inWiki = (repo?: string) => {
      const w = repo ? wikis.get(repo) : undefined;
      return w?.get(t) ?? w?.get(lower) ?? null;
    };
    const note = () => named.get(t) ?? namedLower.get(lower) ?? null;
    if (from.kind === "page") return inWiki(from.repo) ?? note();
    return note() ?? (from.scope ? inWiki(wikiOf(from.scope)) : null);
  }

  const edges: Edge[] = [];
  const broken: Broken[] = [];
  const seen = new Set<string>();
  const add = (e: Edge) => {
    const k = e.from + "\n" + e.to + "\n" + e.type;
    if (e.from === e.to || seen.has(k)) return;
    seen.add(k);
    edges.push(e);
  };
  for (const it of items) {
    const lost = new Set<string>();
    for (const l of parseLinks(it.body)) {
      const to = resolve(it, l.target);
      if (to) add({ from: it.id, to: to.id, type: "link" });
      else if (!lost.has(l.target)) { lost.add(l.target); broken.push({ from: it.id, target: l.target }); }
    }
    if (it.kind === "page" && it.parent) {
      const par = wikis.get(it.repo!)?.get(it.parent);
      if (par) add({ from: it.id, to: par.id, type: "part of" });
    }
  }
  const out = new Map<string, Edge[]>(), into = new Map<string, Edge[]>();
  for (const it of items) { out.set(it.id, []); into.set(it.id, []); }
  for (const e of edges) { out.get(e.from)!.push(e); into.get(e.to)!.push(e); }
  const lonely = items.filter(it => !out.get(it.id)!.length && !into.get(it.id)!.length);
  return { items, byId, edges, out, in: into, broken, lonely, resolve };
}

// A link's destination, encoded whole: encodeURIComponent leaves ( ) ! ' * as they are; kept out,
// they cannot end an address early or close the attribute.
const encodeHref = (s: string) => encodeURIComponent(s).replace(/[()!'*]/g, c => "%" + c.charCodeAt(0).toString(16).toUpperCase());

/** The item an in-app link names (#wiki:<id>), or "" when it is no such link. */
export function linkTarget(href: string): string {
  if (!href.startsWith("#wiki:")) return "";
  try { return decodeURIComponent(href.slice(6)); } catch { return ""; }
}

/** The items around one (itself, and those an edge joins it to), with the edges among them. */
export function neighbourhood(g: Graph, id: string): { ids: string[]; edges: Edge[] } {
  const ids = new Set([id]);
  for (const e of g.out.get(id) ?? []) ids.add(e.to);
  for (const e of g.in.get(id) ?? []) ids.add(e.from);
  return { ids: [...ids], edges: g.edges.filter(e => ids.has(e.from) && ids.has(e.to)) };
}

/** Whether an item matches a search: every word in its title, key or text (any case). */
export function matches(it: Item, q: string): boolean {
  const words = q.toLowerCase().split(/\s+/).filter(Boolean);
  if (!words.length) return true;
  const hay = (it.title + "\n" + it.key + "\n" + it.body).toLowerCase();
  return words.every(w => hay.includes(w));
}
