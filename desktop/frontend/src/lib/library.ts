import type { ArtifactRef } from "./state.svelte";

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

/** What the viewer opens for an item: a reply's file, or a file of this Mac; null for a web link. */
export function refOf(a: LibItem): ArtifactRef | null {
  if (a.kind === "file") return { msg: a.msg, name: a.name };
  if (a.path) return { name: a.name, path: a.path };
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
