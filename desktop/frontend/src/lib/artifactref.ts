import type { ArtifactRef } from "./state.svelte";

// One rule for what an artifact reference is: a reply's file by its message and name; a file of
// this Mac by its path, made absolute and plain (".", ".." taken out) when it is opened, so the same
// file reached from two documents is one reference, and two files of the same relative name in
// two projects are two.

/** A path with "." and ".." resolved, as the system reads it ("/a/b/../c" is "/a/c"). */
function plain(p: string): string {
  const out: string[] = [];
  for (const part of p.split("/")) {
    if (part === "" || part === ".") continue;
    if (part === "..") out.pop(); else out.push(part);
  }
  return "/" + out.join("/");
}

/** A reference as it is kept: a local path absolute when its base (or, for ~/…, home: the home
 * directory) says where it is from. */
export function normalizeRef(a: ArtifactRef, home = ""): ArtifactRef {
  if (!a.path) return a;
  let p = a.path.trim().replace(/^file:\/\//, "");
  // The home directory's, wherever the link is from; without it, the app's side expands it.
  if (p === "~" || p.startsWith("~/")) {
    if (!home.startsWith("/")) return { ...a, path: p };
    p = home.replace(/\/$/, "") + p.slice(1);
  }
  if (!p.startsWith("/") && a.base?.startsWith("/")) p = a.base.replace(/\/$/, "") + "/" + p;
  if (!p.startsWith("/")) return { ...a, path: p }; // ~/… or no base: the app's side resolves it
  const path = plain(p);
  return { ...a, name: path.split("/").pop() || a.name, path, base: "" };
}

/** A reference's identity: equal keys are the same file. */
export function refKey(a: ArtifactRef): string {
  return a.path ? `p:${a.base ?? ""}|${a.path}` : `m:${a.msg ?? ""}|${a.name}`;
}

export const sameRef = (a: ArtifactRef | null | undefined, b: ArtifactRef | null | undefined) => !!a && !!b && refKey(a) === refKey(b);

/** The directory a file's relative links lead from. */
export const dirOf = (path: string) => path.slice(0, path.lastIndexOf("/")) || "/";
