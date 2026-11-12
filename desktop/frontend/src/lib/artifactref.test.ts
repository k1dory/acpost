import { describe, it, expect } from "vitest";
import { normalizeRef, refKey, sameRef, dirOf } from "./artifactref";

describe("artifact references", () => {
  it("are one per file: a local path made absolute and plain", () => {
    const a = normalizeRef({ name: "notes.md", path: "docs/notes.md", base: "/project-a" });
    expect(a).toEqual({ name: "notes.md", path: "/project-a/docs/notes.md", base: "" });
    expect(sameRef(a, normalizeRef({ name: "x", path: "file:///project-a/sub/../docs/./notes.md" }))).toBe(true);
    expect(sameRef(a, normalizeRef({ name: "notes.md", path: "docs/notes.md", base: "/project-b" }))).toBe(false);
    // Nothing to resolve against: the app's side does; the base is part of what it is.
    const rel = normalizeRef({ name: "n", path: "~/n.md", base: "" });
    expect(rel.path).toBe("~/n.md");
    expect(refKey(normalizeRef({ name: "n", path: "n.md", base: "rel" }))).not.toBe(refKey(normalizeRef({ name: "n", path: "n.md", base: "" })));
  });
  it("of a reply's file are by message and name", () => {
    expect(sameRef({ msg: "m", name: "a" }, { msg: "m", name: "a" })).toBe(true);
    expect(sameRef({ msg: "m", name: "a" }, { msg: "n", name: "a" })).toBe(false);
    expect(sameRef(null, { msg: "m", name: "a" })).toBe(false);
    expect(dirOf("/a/b.md")).toBe("/a");
    expect(dirOf("/b.md")).toBe("/");
  });
});
