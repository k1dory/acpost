import { describe, it, expect } from "vitest";
import { parseLinks, buildGraph, renderWiki, pageId } from "./wikigraph";

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

  it("a code span in a cell holding [[a|b]] stays one cell, its text whole, and holds no link", () => {
    // Not GFM's split: a wiki link's pipe is masked wherever its [[ is on a row, in code too
    // (round 6, the table tokenizer), and the code span keeps the pipe.
    const { doc, links } = both("| a | b |\n|---|---|\n| `[[arch|x]]` | y |");
    expect(links).toEqual([]);
    expect(doc.querySelectorAll("a")).toHaveLength(0);
    expect(cells(doc)[1]).toEqual(["[[arch|x]]", "y"]);
    expect(doc.querySelector("td code")?.textContent).toBe("[[arch|x]]");
  });

  it("leaves a fenced copy of a table as it is, before the real one", () => {
    const table = "| Page | About |\n|---|---|\n| [[arch|Architecture]] | Details |";
    const { doc, links } = both("```\n" + table + "\n```\n\n" + table);
    expect(doc.querySelector("pre code")?.textContent).toBe(table + "\n");
    expect(links).toEqual([{ target: "arch", label: "Architecture" }]);
    expect(cells(doc)).toEqual([["Page", "About"], ["Architecture", "Details"]]);
  });
});
