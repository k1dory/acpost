import { describe, it, expect } from "vitest";
import { cardKind, firstLines, itemKey, refOf, typeLabel, webLink, type LibItem } from "./library";

const item = (o: Partial<LibItem>): LibItem => ({ kind: "file", name: "a", size: 1, msg: "m1", at: "", ...o });

describe("the library's items", () => {
  it("show a picture, the start of a text, or only their type", () => {
    expect(cardKind(item({ name: "chart.png", type: "image/png" }))).toBe("image");
    expect(cardKind(item({ kind: "change", name: "logo.SVG", path: "/w/logo.SVG" }))).toBe("image");
    expect(cardKind(item({ name: "notes.md", type: "text/markdown; charset=utf-8" }))).toBe("text");
    expect(cardKind(item({ name: "data.csv" }))).toBe("text");
    expect(cardKind(item({ name: "index.html", type: "text/html" }))).toBe("");   // a page is not shown as its source
    expect(cardKind(item({ name: "report.pdf", type: "application/pdf" }))).toBe("");
    expect(cardKind(item({ kind: "change", name: "x.png", path: "/w/x.png", gone: true }))).toBe(""); // nothing to ask for
    expect(cardKind(item({ kind: "link", name: "x.png", uri: "https://example.com/x.png" }))).toBe(""); // the web is not fetched
  });

  it("open as a reply's file, a file of this Mac, or a web page", () => {
    expect(refOf(item({ name: "a.md" }))).toEqual({ msg: "m1", name: "a.md", base: "" });
    expect(refOf(item({ name: "a.md", cwd: "/p" }))).toEqual({ msg: "m1", name: "a.md", base: "/p" });
    expect(refOf(item({ kind: "change", name: "r.md", path: "/w/r.md" }))).toEqual({ name: "r.md", path: "/w/r.md", base: "/w" });
    expect(refOf(item({ kind: "link", name: "r.md", uri: "file:///w/r.md", path: "/w/r.md" }))).toEqual({ name: "r.md", path: "/w/r.md", base: "/w" });
    const web = item({ kind: "link", name: "site", uri: "https://example.com/" });
    expect(refOf(web)).toBeNull();
    expect(webLink(web)).toBe("https://example.com/");
    expect(webLink(item({ kind: "link", uri: "javascript:alert(1)" }))).toBe("");
  });

  it("name their type and have keys of their own", () => {
    expect(typeLabel(item({ name: "a.markdown" }))).toBe("MARKDOWN");
    expect(typeLabel(item({ name: "noext", type: "application/pdf" }))).toBe("PDF");
    expect(firstLines("1\r\n2\n3\n4", 2)).toBe("1\n2");
    expect(itemKey(item({ name: "a" }))).not.toBe(itemKey(item({ kind: "link", name: "a", uri: "file:///a" })));
  });
});
