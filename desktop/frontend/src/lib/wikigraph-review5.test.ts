import { describe, it, expect } from "vitest";
import { parseLinks, buildGraph, renderWiki, tableSafe, pageId } from "./wikigraph";

// Review round 5: a labelled wiki link in a GFM table row stays one link in one cell, for the
// graph and the page alike; the body outside tables is not touched.
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

describe("wiki links in tables", () => {
  it("a labelled link in a row: one link, its edge, both cells", () => {
    const { g, p, doc, links } = both("| Page | About |\n|---|---|\n| [[arch|Architecture]] | Details |");
    expect(links).toEqual([{ target: "arch", label: "Architecture" }]);
    expect(g.out.get(p.id)!.map(e => e.to)).toEqual([pageId("/r", "arch")]);
    expect(cells(doc)).toEqual([["Page", "About"], ["Architecture", "Details"]]);
    expect([...doc.querySelectorAll("a")].map(a => a.getAttribute("href"))).toEqual([archHref]);
  });

  it("in a quote, in a list item, and in the header row", () => {
    for (const body of [
      "> | Page | About |\n> |---|---|\n> | [[arch|Architecture]] | Details |",
      "- item\n\n  | Page | About |\n  |---|---|\n  | [[arch|Architecture]] | Details |",
      "| [[arch|Architecture]] | About |\n|---|---|\n| x | Details |",
    ]) {
      const { doc, links } = both(body);
      expect(links.map(l => l.label)).toEqual(["Architecture"]);
      expect(cells(doc).flat()).toContain("Details");
      expect(cells(doc).flat()).toContain("Architecture");
      expect(doc.querySelectorAll("a")).toHaveLength(1);
    }
  });

  it("an escaped pipe the author wrote, and two links in one cell", () => {
    let r = both("| a | b |\n|---|---|\n| [[arch\\|Label]] | x |");
    expect(r.links).toEqual([{ target: "arch", label: "Label" }]);
    expect(cells(r.doc)[1]).toEqual(["Label", "x"]);
    r = both("| a | b |\n|---|---|\n| [[arch|One]] and [[real|Two]] | x |");
    expect(r.links.map(l => l.target)).toEqual(["arch", "real"]);
    expect(cells(r.doc)[1]).toEqual(["One and Two", "x"]);
    // Outside a table, an escaped pipe is a link too.
    expect(parseLinks("see [[arch\\|Label]]")).toEqual([{ target: "arch", label: "Label" }]);
  });

  it("a code span in a cell follows GFM: its pipe splits the cell, and it holds no link", () => {
    const { doc, links } = both("| a | b |\n|---|---|\n| `[[arch|x]]` | y |");
    expect(links).toEqual([]);
    expect(doc.querySelectorAll("a")).toHaveLength(0);
    // The pipe ended the first cell: the code span is cut and "y" is a third, dropped column.
    expect(cells(doc)[1]).toEqual(["`[[arch", "x]]`"]);
  });

  it("leaves the body outside tables as it is", () => {
    const body = "```\n| [[arch|x]] | y |\n```\n\n`[[arch|x]]` and [[arch|x]]\n\n| a | b |\n|---|---|\n| [[arch|x]] | y |";
    const out = tableSafe(body);
    expect(out.slice(0, out.lastIndexOf("| a |"))).toBe(body.slice(0, body.lastIndexOf("| a |")));
    expect(out.endsWith("| [[arch\\|x]] | y |")).toBe(true);
  });
});
