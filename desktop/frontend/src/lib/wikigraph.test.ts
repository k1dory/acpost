import { describe, it, expect } from "vitest";
import { parseLinks, buildGraph, linkify, linkTarget, neighbourhood, matches, pageId, noteId } from "./wikigraph";

const pages = [
  { repo: "/r", slug: "overview", title: "Overview", body: "See [[arch]] and [[arch|the design]], [[style]], [[nowhere]] and [[overview]]." },
  { repo: "/r", slug: "arch", title: "Architecture", body: "```\n[[in-code]]\n```\nText `[[inline]]` and [[ Overview ]]." },
  { repo: "/r", slug: "store", title: "Store", parent: "arch", body: "" },
  { repo: "/s", slug: "overview", title: "Other", body: "[[arch]]" },
  { repo: "/s", slug: "alone", title: "Alone", body: "" },
];
const notes = [
  { name: "style", body: "Short. See [[store]] and [[Missing note]].", scope: "/r/sub", enabled: true },
  { name: "Go", body: "[[STYLE]]", enabled: true },
];

describe("wiki links", () => {
  it("finds [[target]] and [[target|label]], not in code", () => {
    expect(parseLinks("a [[x]] b [[y|Why]] [[ z ]] [[]] [[a|]]")).toEqual([
      { target: "x" }, { target: "y", label: "Why" }, { target: "z" }, { target: "a" },
    ]);
    expect(parseLinks("```go\n[[no]]\n```\n~~~\n[[no]]\n~~~\n`[[no]]` ``a [[no]]`` [[yes]]")).toEqual([{ target: "yes" }]);
    // A fence closed by a shorter one is not closed.
    expect(parseLinks("````\n```\n[[no]]\n````\n[[yes]]")).toEqual([{ target: "yes" }]);
    // An unmatched backtick is text.
    expect(parseLinks("a ` [[yes]]")).toEqual([{ target: "yes" }]);
  });

  it("resolves in the page's own wiki first, then notes by name; a note's in notes, then its directory's wiki", () => {
    const g = buildGraph(pages, notes);
    const out = (id: string) => g.out.get(id)!.map(e => `${e.type} ${e.to}`).sort();
    expect(out(pageId("/r", "overview"))).toEqual([`link ${noteId("style")}`, `link ${pageId("/r", "arch")}`]);
    // Its own wiki's arch, not /r's; the case of a target does not matter when nothing matches it exactly.
    expect(out(pageId("/s", "overview"))).toEqual([]);
    expect(g.broken).toContainEqual({ from: pageId("/s", "overview"), target: "arch" });
    expect(out(pageId("/r", "arch"))).toEqual([`link ${pageId("/r", "overview")}`]);
    expect(out(pageId("/r", "store"))).toEqual([`part of ${pageId("/r", "arch")}`]);
    expect(out(noteId("style"))).toEqual([`link ${pageId("/r", "store")}`]);
    expect(out(noteId("Go"))).toEqual([`link ${noteId("style")}`]);
  });

  it("keeps backlinks, broken links once each, no edge to itself, and the items on their own", () => {
    const g = buildGraph(pages, notes);
    const back = (id: string) => g.in.get(id)!.map(e => e.from).sort();
    expect(back(pageId("/r", "arch"))).toEqual([pageId("/r", "overview"), pageId("/r", "store")]);
    expect(back(noteId("style"))).toEqual([noteId("Go"), pageId("/r", "overview")].sort());
    // [[arch]] and [[arch|the design]]: one edge.
    expect(g.edges.filter(e => e.from === pageId("/r", "overview") && e.to === pageId("/r", "arch"))).toHaveLength(1);
    expect(g.edges.some(e => e.from === e.to)).toBe(false);
    expect(g.broken.map(b => b.target).sort()).toEqual(["Missing note", "arch", "nowhere"]);
    // A page whose only link is broken is on its own too.
    expect(g.lonely.map(i => i.id)).toEqual([pageId("/s", "overview"), pageId("/s", "alone")]);
  });

  it("makes links the app routes, and marks broken ones", () => {
    const g = buildGraph(pages, notes);
    const md = linkify(pages[0].body, g.byId.get(pageId("/r", "overview"))!, g);
    expect(md).toContain(`[Architecture](#wiki:${encodeURIComponent(pageId("/r", "arch"))})`);
    expect(md).toContain(`[the design](#wiki:`);
    expect(md).toContain(`[nowhere](#wiki-broken:nowhere)`);
    expect(linkTarget(`#wiki:${encodeURIComponent(pageId("/r", "arch"))}`)).toBe(pageId("/r", "arch"));
    expect(linkTarget("#wiki-broken:x")).toBe("");
    // Code stays code; brackets in a label do not break the link.
    const arch = g.byId.get(pageId("/r", "arch"))!;
    expect(linkify(arch.body, arch, g)).toContain("```\n[[in-code]]\n```");
    expect(linkify("[[overview|a [b]]]", arch, g)).not.toContain("#wiki:");
    expect(linkify("[[overview|a]b]]", arch, g)).toBe("[[overview|a]b]]");
  });

  it("finds an item's neighbours, and searches title, key and text", () => {
    const g = buildGraph(pages, notes);
    const nb = neighbourhood(g, pageId("/r", "arch"));
    expect(nb.ids.sort()).toEqual([pageId("/r", "arch"), pageId("/r", "overview"), pageId("/r", "store")].sort());
    expect(nb.edges.every(e => nb.ids.includes(e.from) && nb.ids.includes(e.to))).toBe(true);
    const it = g.byId.get(noteId("style"))!;
    expect(matches(it, "SHORT style")).toBe(true);
    expect(matches(it, "short nothing")).toBe(false);
    expect(matches(it, "  ")).toBe(true);
  });
});
