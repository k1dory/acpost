import type { ArtifactRef } from "./state.svelte";
import { normalizeRef } from "./artifactref";

/** An artifact of the library, as the node lists it: a file a reply carries (file), a reply's link
 * (link), or a document a turn created or edited on disk (change). */
export type LibItem = {
  kind: "file" | "link" | "change";
  name: string;
  type?: string;
  size: number; // -1: not known
  uri?: string;
  path?: string;
  status?: string;
  msg: string;
  thread?: string;
  title?: string;
  cwd?: string; // the chat's directory
  agent?: string;
  at: string;
  gone?: boolean;
};

const ext = (name: string) => (name.match(/\.([a-z0-9]+)$/i)?.[1] ?? "").toLowerCase();
const media = (a: LibItem) => (a.type ?? "").split(";")[0].trim().toLowerCase();

/** How a card shows an item: a picture, the start of a text, or "" (its type only). A page is not
 * shown as its source. */
export function cardKind(a: LibItem): "image" | "text" | "" {
  if (a.gone || webLink(a)) return "";
  const ty = media(a), e = ext(a.name);
  if (ty === "text/html" || e === "html" || e === "htm") return "";
  if (ty.startsWith("image/") || ["png", "jpg", "jpeg", "gif", "webp", "svg"].includes(e)) return "image";
  if (ty.startsWith("text/") || ty === "application/json" || ["md", "markdown", "csv", "json", "txt"].includes(e)) return "text";
  return "";
}

/** What the viewer opens for an item: a reply's file, or a file of this Mac; null for a web link.
 * A reply's file reads its relative links from its chat's directory; a file of this Mac from its own. */
export function refOf(a: LibItem): ArtifactRef | null {
  if (a.kind === "file") return { msg: a.msg, name: a.name, base: a.cwd ?? "" };
  if (a.path) return normalizeRef({ name: a.name, path: a.path });
  return null;
}

/** An item's web address, when it is a link to a page on the web. */
export function webLink(a: LibItem): string {
  return a.kind === "link" && /^https?:\/\//i.test(a.uri ?? "") ? a.uri! : "";
}

/** The first lines of a text, for a card. */
export function firstLines(text: string, n = 8): string {
  return text.replace(/\r\n/g, "\n").split("\n").slice(0, n).join("\n");
}

/** A short name of an item's type: its extension, else its media type's last part. */
export function typeLabel(a: LibItem): string {
  return (ext(a.name) || media(a).split("/").pop() || "").toUpperCase().slice(0, 8);
}

/** An item's key in the list: one per file of a reply, one per document on disk. */
export const itemKey = (a: LibItem) => `${a.kind}|${a.msg}|${a.name}|${a.path ?? a.uri ?? ""}`;

/** What an item is, for the library's filter: a site (a page, a web link), a picture, or a
 * document (everything else). */
export function sortOf(a: LibItem): "site" | "image" | "doc" {
  const ty = media(a), e = ext(a.name);
  if (webLink(a) || ty === "text/html" || e === "html" || e === "htm") return "site";
  if (ty.startsWith("image/") || ["png", "jpg", "jpeg", "gif", "webp", "svg"].includes(e)) return "image";
  return "doc";
}

/** The library's list: the chats' artifacts (a page of them, newest first) with what was found in
 * the agents' working folders of this Mac. While older pages of the chats' are still to come
 * (more), a found file older than the oldest listed waits for its page; a file both list is the
 * chat's. */
export function withFound(listed: LibItem[], found: LibItem[], more: boolean): LibItem[] {
  if (!found.length) return listed;
  const at = (a: LibItem) => Date.parse(a.at) || 0;
  const have = new Set(listed.map(a => a.path).filter(Boolean));
  const oldest = more && listed.length ? Math.min(...listed.map(at)) : more ? Infinity : -Infinity;
  const extra = found.filter(f => !have.has(f.path) && at(f) >= oldest);
  return [...listed, ...extra].map((a, i) => ({ a, i })).sort((x, y) => at(y.a) - at(x.a) || x.i - y.i).map(x => x.a);
}
