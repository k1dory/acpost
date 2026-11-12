import { describe, it, expect } from "vitest";
import { parseLinks, buildGraph, renderWiki, tableScan, pageId } from "./wikigraph";

// Review round 8: marked asks the table tokenizer at every block; it reads a block's first two
// lines to say no, and a table's own lines to read it — linear in the body, never quadratic.
const paragraphs = (n: number) => Array.from({ length: n }, (_, i) => `Paragraph ${i} of plain text, long enough to be like a page's: it says nothing much.`).join("\n\n");
const table = "\n\n| Page | About |\n|---|---|\n| [[arch|Architecture]] | Details |";
const resolve = (t: string) => (t === "arch" ? { id: pageId("/r", "arch"), title: "Arch" } : null);
function scanned(body: string) {
  tableScan.chars = 0;
  parseLinks(body);
  return tableScan.chars;
}

describe("the table tokenizer's work", () => {
  it("grows with the body, not with its square", () => {
    const small = scanned(paragraphs(2000)), large = scanned(paragraphs(20000));
    expect(small).toBeGreaterThan(0);
    // Ten times the body, about ten times the work (a quadratic one would be about a hundred).
    expect(large / small).toBeLessThan(13);
    expect(large).toBeLessThan(paragraphs(20000).length * 2);
  });

  it("20,000 paragraphs, and the same with a table at the end, are read and rendered within a bound", () => {
    for (const body of [paragraphs(20000), paragraphs(20000) + table]) {
      const t0 = performance.now();
      const g = buildGraph([{ repo: "/r", slug: "p", title: "P", body }, { repo: "/r", slug: "arch", title: "Arch" }], []);
      const html = renderWiki(body, resolve);
      const ms = performance.now() - t0;
      expect(ms).toBeLessThan(2000); // was ~8 s
      const linked = body.endsWith("Details |");
      expect(g.out.get(pageId("/r", "p"))!.map(e => e.to)).toEqual(linked ? [pageId("/r", "arch")] : []);
      if (linked) {
        expect(html).toContain(`<td><a href="#wiki:${encodeURIComponent(pageId("/r", "arch"))}">Architecture</a></td>`);
        expect(html).toContain("<td>Details</td>");
      }
      expect(scanned(body)).toBeLessThan(body.length * 2);
    }
  }, 30000);

  it("still reads a table that ends at the body's end, before a blank line, or before more text", () => {
    for (const tail of ["", "\n\nafter", "\n"]) {
      const links = parseLinks("| a | b |\n|---|---|\n| [[arch|A]] | y |" + tail);
      expect(links).toEqual([{ target: "arch", label: "A" }]);
    }
    // A second line that only looks like a delimiter (a setext heading) is no table.
    expect(parseLinks("Title [[arch|A]]\n---")).toEqual([{ target: "arch", label: "A" }]);
  });
});
