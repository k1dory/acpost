import { describe, it, expect } from "vitest";
import { sortOf, withFound, type LibItem } from "./library";

const it_ = (o: Partial<LibItem>): LibItem => ({ kind: "change", name: "x", size: 1, msg: "", at: "2026-11-13T10:00:00Z", ...o });

describe("the library's found files", () => {
  it("sorts an item as a site, a picture or a document", () => {
    expect(sortOf(it_({ name: "landing/index.html" }))).toBe("site");
    expect(sortOf(it_({ kind: "link", name: "site", uri: "https://example.com/" }))).toBe("site");
    expect(sortOf(it_({ name: "shot.PNG" }))).toBe("image");
    expect(sortOf(it_({ name: "report.pdf" }))).toBe("doc");
    expect(sortOf(it_({ name: "plan.docx" }))).toBe("doc");
  });
  it("lists found files with the chats', newest first, a file of both once", () => {
    const listed = [it_({ name: "a.md", path: "/w/a.md", msg: "m1", at: "2026-11-13T09:00:00Z" })];
    const found = [
      it_({ name: "new.pdf", path: "/t/new.pdf", at: "2026-11-13T11:00:00Z" }),
      it_({ name: "a.md", path: "/w/a.md", at: "2026-11-13T12:00:00Z" }),
      it_({ name: "old.png", path: "/t/old.png", at: "2026-11-01T00:00:00Z" }),
    ];
    expect(withFound(listed, found, false).map(a => a.path)).toEqual(["/t/new.pdf", "/w/a.md", "/t/old.png"]);
    expect(withFound(listed, found, false)[1].msg).toBe("m1");
  });
  it("holds a found file older than the page until its page comes", () => {
    const listed = [it_({ path: "/w/a.md", at: "2026-11-13T09:00:00Z" })];
    const found = [it_({ path: "/t/new.pdf", at: "2026-11-13T11:00:00Z" }), it_({ path: "/t/old.png", at: "2026-11-01T00:00:00Z" })];
    expect(withFound(listed, found, true).map(a => a.path)).toEqual(["/t/new.pdf", "/w/a.md"]);
    expect(withFound([], found, false).map(a => a.path)).toEqual(["/t/new.pdf", "/t/old.png"]);
  });
});
