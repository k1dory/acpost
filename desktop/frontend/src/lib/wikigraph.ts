// What the agents keep, as one set: the pages of every repository's wiki and the user's knowledge
// notes, with the links between them. A body links to another item as [[target]] or
// [[target|label]]: a page's target is first a page of its own wiki (by slug), then a note (by
// name); a note's is a note, then a page of the wiki of its directory. A page is also part of its
// parent. A link to nothing is kept as broken, so it can be shown and fixed.

import { Marked, Tokenizer, type MarkedExtension, type Token, type Tokens, type TokenizerAndRendererExtension } from "marked";

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

// Between target and label: a pipe, an escaped one (\|, as a table row needs it), or — while one
// parse lexes a table — the mask that parse gave the link's pipe (wikiTable). The mask is a
// private-use character the body does not hold, chosen for each parse (pickMask): one the author
// wrote is never a separator, nor turned into anything.
type MaskState = { mask: string };
const labelPart = "((?:[^[\\]\\n]|\\[[^[\\]\\n]*\\]){0,200})";
const rules = new Map<string, RegExp>();
function wikiRuleFor(mask: string): RegExp {
  let r = rules.get(mask);
  if (!r) {
    const sep = mask ? `(?:\\\\?\\||${mask})` : "\\\\?\\|";
    r = new RegExp(`^\\[\\[([^[\\]|\\n${mask}]{1,200}?)(?:${sep}${labelPart})?\\]\\]`);
    rules.set(mask, r);
  }
  return r;
}
/** A private-use character the body does not hold: the mask for its parse ("" when all are used). */
export function pickMask(body: string): string {
  // U+E000 upwards; a body holding every one of the 6400 has no mask, and its tables are read as
  // marked reads them (a labelled link's pipe splits its cell).
  for (let c = 0xe000; c <= 0xf8ff; c++) {
    const ch = String.fromCharCode(c);
    if (!body.includes(ch)) return ch;
  }
  return "";
}
const escHtml = (s: string) => s.replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");

/**
 * The wiki link as a marked inline extension: marked tokenizes it where it tokenizes text, so a
 * [[link]] in a code block, a code span or HTML is never one, and the graph (parseLinks) and the
 * page (Markdown with resolve) read a body the same way. The renderer makes an in-app link —
 * #wiki:<id> to an item, #wiki-broken:<target> for one that names nothing.
 */
export function wikiLinkExtension(resolve?: WikiResolve, state: MaskState = { mask: "" }): TokenizerAndRendererExtension {
  return {
    name: "wikilink",
    level: "inline",
    start: (src: string) => src.indexOf("[["),
    tokenizer(src: string) {
      const m = wikiRuleFor(state.mask).exec(src);
      const target = m?.[1].trim();
      if (!m || !target) return undefined;
      const label = m[2]?.trim() || undefined;
      const tok: WikiLinkToken = { type: "wikilink", raw: m[0], target, label, tokens: label ? this.lexer.inlineTokens(label) : [] };
      return tok;
    },
    renderer(token) {
      const tok = token as unknown as WikiLinkToken;
      const to = resolve?.(tok.target) ?? null;
      const inner = tok.label ? renderLabel(tok.tokens) : escHtml(to ? to.title : tok.target);
      const href = to ? "#wiki:" + encodeHref(to.id) : "#wiki-broken:" + encodeHref(tok.target);
      return `<a href="${href}">${inner}</a>`;
    },
  };
}

/**
 * A label, rendered here and not by marked's renderers (whose text may come through as it is, as
 * HTML): text, code spans, emphasis, strong, strike-through and escapes, each escaped from its own
 * source; anything else — a link, an autolinked address or e-mail, a picture, HTML — is its source
 * as text. A label never holds a link of its own (the click is the wiki link's) nor markup.
 */
export function renderLabel(tokens: Token[]): string {
  return tokens.map(t => {
    const tok = t as Token & { tokens?: Token[]; text?: string };
    switch (tok.type) {
      case "text": return escHtml(tok.raw);
      case "codespan": return `<code>${escHtml(tok.text ?? "")}</code>`;
      case "escape": return escHtml(tok.text ?? "");
      case "em": case "strong": case "del": return `<${tok.type}>${renderLabel(tok.tokens ?? [])}</${tok.type}>`;
      default: return escHtml(tok.raw);
    }
  }).join("");
}

/**
 * GFM tables, with wiki links in their cells: a row is split into cells at every unescaped pipe
 * before any inline syntax is read, so [[a|b]] would be cut in two. Marked's own table tokenizer is
 * given the text with each wiki link's pipe masked (a link's [[ anywhere on a row, its label code
 * or not), and the mask is turned back into the pipe in what it makes (restoreIn). A fence is
 * never a table, so a table shown as code is left as it is; tables in quotes and lists come here
 * too. A code span in a cell that holds [[a|b]] is masked as well: it stays one cell (not GFM's
 * split), its text whole.
 */
const linkPipe = /\[\[([^[\]|\n]*[^[\]|\n\\])\|((?:[^[\]\n]|\[[^[\]\n]*\]){0,200})\]\]/g;
const stockTable = Tokenizer.prototype.table;
// The work the table tokenizer does: the characters it reads, counted for a test of its bound.
export const tableScan = { chars: 0 };
// A GFM delimiter row: dashes, colons, pipes and spaces, with a dash and a pipe or colon.
const delimiterRow = /^[ \t]*[|:-][ \t|:-]*$/;
function wikiTable(state: MaskState) {
  return function (this: Tokenizer, src: string): Tokens.Table | false {
    const mask = state.mask;
    if (!mask) return false; // no mask for this body: marked's own table, as it is
    // Marked asks at every block: only one whose second line is a delimiter row can be a table, and
    // finding that out reads its first two lines, never the rest of the body.
    const nl = src.indexOf("\n");
    tableScan.chars += nl < 0 ? src.length : nl + 1;
    if (nl < 0) return false;
    const nl2 = src.indexOf("\n", nl + 1);
    const second = src.slice(nl + 1, nl2 < 0 ? src.length : nl2);
    tableScan.chars += second.length;
    if (!second.includes("-") || !/[|:]/.test(second) || !delimiterRow.test(second)) return false;
    // The table's own lines: to the first blank line (where a table ends) or the end.
    const blank = /\n[ \t]*(?:\n|$)/g;
    blank.lastIndex = nl2 < 0 ? src.length : nl2;
    const end = blank.exec(src)?.index ?? src.length;
    const block = src.slice(0, end < src.length ? end + 1 : end);
    tableScan.chars += block.length;
    const masked = block.replace(linkPipe, (_all, target: string, label: string) => `[[${target}${mask}${label}]]`);
    if (masked === block) return false; // no wiki link: marked's own, as it is
    const tok = stockTable.call(this, masked);
    if (!tok) return false;
    tok.raw = src.slice(0, tok.raw.length); // the mask is one character for one: the same length
    return tok;
  };
}
// The mask, put back as the pipe it stands for: within a table only (its cells and their tokens),
// and only this parse's mask — which the body does not hold, so nothing the author wrote changes.
function restoreIn(table: Token, mask: string) {
  if (!mask) return;
  const fix = (v: unknown) => {
    if (Array.isArray(v)) { for (const x of v) fix(x); return; }
    if (!v || typeof v !== "object") return;
    const o = v as Record<string, unknown>;
    for (const k of Object.keys(o)) {
      const x = o[k];
      if (typeof x === "string") { if (x.includes(mask)) o[k] = x.replaceAll(mask, "|"); }
      else if (typeof x === "object") fix(x);
    }
  };
  fix(table);
}

/**
 * Each token of a tree, in document order — a token before its own: inline tokens, list items,
 * table cells, a wiki link's label.
 */
function eachToken(tokens: Token[], fn: (t: Token) => void) {
  const visit = (v: unknown) => {
    if (Array.isArray(v)) { for (const x of v) visit(x); return; }
    if (!v || typeof v !== "object") return;
    const o = v as Record<string, unknown>;
    if (typeof o.type === "string") fn(o as Token);
    for (const k of ["header", "rows", "items", "tokens"]) if (o[k]) visit(o[k]);
  };
  visit(tokens);
}

/**
 * The wiki's Markdown for marked: the link, and tables that keep it in one cell. Its mask is the
 * instance's own and is chosen at each parse (preprocess) and dropped after it (postprocess): a
 * parse never sees another body's.
 */
export function wikiMarkdown(resolve?: WikiResolve, state: MaskState = { mask: "" }): MarkedExtension {
  return {
    extensions: [wikiLinkExtension(resolve, state)],
    tokenizer: { table: wikiTable(state) },
    hooks: {
      preprocess: (body: string) => { state.mask = pickMask(body); return body; },
      // Not walkTokens: marked gathers its callbacks' results into an array copied at each token,
      // which is quadratic in the body (20,000 paragraphs: seconds); this walk is linear.
      processAllTokens: tokens => { eachToken(tokens, t => { if (t.type === "table") restoreIn(t, state.mask); }); return tokens; },
      postprocess: (html: string) => { state.mask = ""; return html; },
    },
  };
}

// The options the app renders Markdown with (components/Markdown.svelte): the graph lexes alike.
export const markdownOptions = { gfm: true, breaks: true } as const;
const readerState: MaskState = { mask: "" };
const reader = new Marked(markdownOptions).use(wikiMarkdown(undefined, readerState));

/** The [[links]] of a body, in order, where Markdown has them (not in code or HTML). */
export function parseLinks(body: string): Link[] {
  const found: Link[] = [];
  let tokens: Token[];
  const text = body ?? "";
  readerState.mask = pickMask(text); // this parse's (lexer() runs no hooks)
  try { tokens = reader.lexer(text); } catch { readerState.mask = ""; return found; } // text marked cannot read: no links
  const mask = readerState.mask;
  readerState.mask = "";
  eachToken(tokens, t => {
    if (t.type === "table") restoreIn(t, mask);
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
