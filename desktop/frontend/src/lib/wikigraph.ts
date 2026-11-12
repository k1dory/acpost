// What the agents keep, as one set: the pages of every repository's wiki and the user's knowledge
// notes, with the links between them. A body links to another item as [[target]] or
// [[target|label]]: a page's target is first a page of its own wiki (by slug), then a note (by
// name); a note's is a note, then a page of the wiki of its directory. A page is also part of its
// parent. A link to nothing is kept as broken, so it can be shown and fixed.

import { Lexer } from "marked";

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

const linkRe = /\[\[([^[\]|\n]{1,200})(?:\|([^[\]\n]{0,200}))?\]\]/g;

/**
 * Where a Markdown text has code — fenced and indented blocks, inline code spans — as [start, end)
 * offsets, found by marked's lexer (the one the app renders with), so the links the graph counts
 * are those the page shows. Text with \r\n is taken as with \n, as the lexer takes it.
 */
export function codeRanges(text: string): [number, number][] {
  const out: [number, number][] = [];
  let tokens: unknown[];
  try { tokens = lexer.lex(text); } catch { return out; } // text the lexer cannot read: no code found in it
  // Each top-level token's raw is the next stretch of the text (they follow one another); a code
  // token inside one is looked for within that stretch only, after the code found before it there.
  let start = 0;
  for (const top of tokens as { raw?: string }[]) {
    const raw = top.raw ?? "";
    const at = text.startsWith(raw, start) ? start : text.indexOf(raw, start);
    if (at < 0) continue;
    const span = { at, end: at + raw.length };
    walk(top, span);
    start = span.end;
  }
  return out;

  // Tokens in the order they are in the text (blocks, then their inline tokens, list items, cells).
  function walk(v: unknown, span: { at: number; end: number }) {
    if (Array.isArray(v)) { for (const x of v) walk(x, span); return; }
    if (!v || typeof v !== "object") return;
    const tok = v as Record<string, unknown>;
    if ((tok.type === "code" || tok.type === "codespan") && typeof tok.raw === "string") { mark(tok.raw, span); return; }
    for (const k of Object.keys(tok)) if (k !== "raw" && typeof tok[k] === "object") walk(tok[k], span);
  }
  function mark(raw: string, span: { at: number; end: number }) {
    const i = text.indexOf(raw, span.at);
    if (i >= 0 && i + raw.length <= span.end) { out.push([i, i + raw.length]); span.at = i + raw.length; return; }
    const r = prefixed(raw.replace(/\n$/, "").split("\n"), span);
    if (r) { out.push(r); span.at = r[1]; }
  }
  // Code in a quote or a list item comes without the lines' markers ("> ", the item's indent): it
  // is the first run of whole lines of the stretch, from where the search is, each ending with the
  // code's line in turn.
  function prefixed(lines: string[], span: { at: number; end: number }): [number, number] | null {
    const starts: number[] = [];
    for (let p = text.lastIndexOf("\n", span.at - 1) + 1; p < span.end; ) {
      starts.push(p);
      const nl = text.indexOf("\n", p);
      p = nl < 0 ? text.length : nl + 1;
    }
    const lineAt = (k: number) => {
      const nl = text.indexOf("\n", starts[k]);
      return text.slice(starts[k], nl < 0 || nl > span.end ? span.end : nl);
    };
    for (let k = 0; k + lines.length <= starts.length; k++) {
      if (!lines.every((l, j) => lineAt(k + j).endsWith(l))) continue;
      const first = lineAt(k), lastEnd = starts[k + lines.length - 1] + lineAt(k + lines.length - 1).length;
      const from = Math.max(starts[k] + first.length - lines[0].length, span.at);
      if (from >= lastEnd && lines.join("")) continue;
      return [from, lastEnd];
    }
    return null;
  }
}
const lexer = { lex: (s: string) => new Lexer({ gfm: true, breaks: true }).lex(s) };

/** Each [[link]] of a text outside code (one whose [[ starts in code is code: a label may be). */
function eachLink(text: string, fn: (m: RegExpExecArray) => void) {
  const code = codeRanges(text);
  const inCode = (i: number) => code.some(([a, b]) => i >= a && i < b);
  linkRe.lastIndex = 0;
  for (let m: RegExpExecArray | null; (m = linkRe.exec(text)); ) if (!inCode(m.index)) fn(m);
}
const lf = (s: string) => (s ?? "").replace(/\r\n?/g, "\n");

/** The [[links]] of a body, in order, outside code. */
export function parseLinks(body: string): Link[] {
  const found: Link[] = [];
  eachLink(lf(body), m => {
    const target = m[1].trim();
    const label = m[2]?.trim();
    if (target) found.push(label ? { target, label } : { target });
  });
  return found;
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

// A link's destination, encoded whole: encodeURIComponent leaves ( ) ! ' * as they are, and a ")"
// would end a Markdown destination early (a repository at /work/app)).
const encodeHref = (s: string) => encodeURIComponent(s).replace(/[()!'*]/g, c => "%" + c.charCodeAt(0).toString(16).toUpperCase());

const escLabel = (s: string) => s.replace(/[\\[\]]/g, m => "\\" + m);

/**
 * A body with its [[links]] made Markdown links the app routes: #wiki:<id> to an item, and
 * #wiki-broken:<target> for one that names nothing (shown marked, going nowhere).
 */
export function linkify(body: string, from: Item, g: Graph): string {
  const text = lf(body);
  let out = "", at = 0;
  eachLink(text, m => {
    const target = m[1], label = m[2];
    const to = g.resolve(from, target);
    const shown = escLabel((label ?? "").trim() || (to ? to.title : target.trim()));
    out += text.slice(at, m.index) + (to ? `[${shown}](#wiki:${encodeHref(to.id)})` : `[${shown}](#wiki-broken:${encodeHref(target.trim())})`);
    at = m.index + m[0].length;
  });
  return out + text.slice(at);
}

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
