import { describe, it, expect } from "vitest";
import { parseLinks, buildGraph, renderWiki, pageId } from "./wikigraph";

// Review round 6: tables are read by marked's own table tokenizer with the links' pipes masked —
// no positions — so a code label stays in its cell and a fenced copy of a table stays code.
const pages = (body: string) => [{ repo: "/r", slug: "p", title: "P", body }, { repo: "/r", slug: "arch", title: "Arch" }, { repo: "/r", slug: "real", title: "Real" }];
function both(body: string) {
  const g = buildGraph(pages(body), []);
  const p = g.byId.get(pageId("/r", "p"))!;
  const html = renderWiki(body, t => g.resolve(p, t));
  const doc = new DOMParser().parseFromString(html, "text/html");
  return { g, p, html, doc, links: parseLinks(body) };
}
const cells = (doc: Document) => [...doc.querySelectorAll("tr")].map(tr => [...tr.children].map(c => c.textContent));
const archHref = "#wiki:" + encodeURIComponent(pageId("/r", "arch"));

describe("wiki links in tables, by marked's table tokenizer", () => {
  it("a code label in a row: one link, its edge, both cells", () => {
    const { g, p, doc, links } = both("| Page | About |\n|---|---|\n| [[arch|`API`]] | Details |");
    expect(links).toEqual([{ target: "arch", label: "`API`" }]);
    expect(g.out.get(p.id)!.map(e => e.to)).toEqual([pageId("/r", "arch")]);
    expect(cells(doc)[1]).toEqual(["API", "Details"]);
    const a = doc.querySelector("td a")!;
    expect(a.getAttribute("href")).toBe(archHref);
    expect(a.querySelector("code")?.textContent).toBe("API");
  });

  it("a fenced example in a quote, then the same table for real: the code is byte-identical, the table has its link", () => {
    const table = "| Page | About |\n|---|---|\n| [[arch|Architecture]] | Details |";
    const q = (s: string) => s.split("\n").map(l => "> " + l).join("\n");
    const { doc, links, g, p } = both(q("```\n" + table + "\n```") + "\n>\n" + q(table));
    expect(doc.querySelector("pre code")?.textContent).toBe(table + "\n");
    expect(links).toEqual([{ target: "arch", label: "Architecture" }]);
    expect(g.out.get(p.id)!.map(e => e.to)).toEqual([pageId("/r", "arch")]);
    expect(cells(doc)).toEqual([["Page", "About"], ["Architecture", "Details"]]);
  });

  it("in the header row, in a quote, in a list item, two in a cell, escaped", () => {
    expect(cells(both("| [[arch|A]] | b |\n|---|---|\n| x | y |").doc)).toEqual([["A", "b"], ["x", "y"]]);
    expect(cells(both("> | a | b |\n> |---|---|\n> | [[arch|A]] | y |").doc)[1]).toEqual(["A", "y"]);
    expect(cells(both("- item\n\n  | a | b |\n  |---|---|\n  | [[arch|A]] | y |").doc)[1]).toEqual(["A", "y"]);
    const two = both("| a | b |\n|---|---|\n| [[arch|One]] and [[real|Two]] | x |");
    expect(cells(two.doc)[1]).toEqual(["One and Two", "x"]);
    expect(two.links.map(l => l.target)).toEqual(["arch", "real"]);
    expect(cells(both("| a | b |\n|---|---|\n| [[arch\\|L]] | x |").doc)[1]).toEqual(["L", "x"]);
  });

  it("puts every masked pipe back: the table's raw, text that is no link", () => {
    const { html } = both("| a | b |\n|---|---|\n| [[ |x]] | y |");
    expect(html).not.toContain("\uE000");
  });
});
