// @vitest-environment happy-dom
import { describe, it, expect } from "vitest";
import { buildGraph, renderWiki, tableScan } from "./wikigraph";

// Tables set apart by headings, with no blank line between: each table's scan ends at the next
// heading, so the work grows with the body, not with its square.
const body = (n: number) => ("# Section\n| A |\n|---|\n| [[arch|A]] |\n").repeat(n);
const entries = (text: string): any[] => [
  { repo: "/r", slug: "p", title: "P", body: text, updated: "" } as any,
  { repo: "/r", slug: "arch", title: "Arch", body: "", updated: "" } as any,
];

describe("tables without blank lines between them", () => {
  it("are scanned in time that grows with the body", () => {
    const work = (n: number) => { tableScan.chars = 0; buildGraph(entries(body(n)), []); return tableScan.chars; };
    const small = work(200), large = work(2000);
    expect(large).toBeLessThan(small * 13);
    expect(large).toBeLessThan(body(2000).length * 4);
  });
  it("still give every table its link, its edge and its cell", () => {
    const g = buildGraph(entries(body(3)), []);
    expect(g.edges.filter(e => e.type !== "part of").length).toBeGreaterThanOrEqual(1);
    const html = renderWiki(body(3), () => ({ id: "page:/r#arch", broken: false }) as any);
    expect((html.match(/<table/g) ?? []).length).toBe(3);
    expect((html.match(/<a /g) ?? []).length).toBe(3);
    expect((html.match(/<h1/g) ?? []).length).toBe(3);
  });
  it("a table ends where another block begins: a list, a quote, a fence, a rule", () => {
    for (const next of ["- item", "> quote", "```\ncode\n```", "---"]) {
      const text = "| A | B |\n|---|---|\n| [[arch|A]] | Details |\n" + next + "\n";
      const html = renderWiki(text, () => ({ id: "page:/r#arch", broken: false }) as any);
      expect(html).toContain("Details");
      expect((html.match(/<a /g) ?? []).length).toBe(1);
      expect((html.match(/<tr/g) ?? []).length).toBe(2); // the header and one row: the next block is not a row
    }
  });
  it("tables set apart by indented code alone are scanned in time that grows with the body", () => {
    const b = (n: number) => ("| A |\n|---|\n| [[arch|A]] |\n    code\n").repeat(n);
    const work = (n: number) => { tableScan.chars = 0; buildGraph(entries(b(n)), []); return tableScan.chars; };
    const small = work(200), large = work(2000);
    expect(large).toBeLessThan(small * 13);
  });
  it("a label that opens raw HTML does not let the text after the link through unescaped", () => {
    // (HTML an author writes after the link is HTML as anywhere in Markdown, cleaned by the page's
    // sanitizer; what must not happen is plain text after the link being taken for raw HTML.)
    for (const label of ["<code>", "<a href=x>", "<pre>", "<script>"]) {
      const html = renderWiki(`[[arch|${label}]] 1 < 2 & 3 > 2`, () => ({ id: "page:/r#arch", broken: false, title: "Arch" }) as any);
      expect(html).toContain("1 &lt; 2 &amp; 3 &gt; 2");
      expect((html.match(/<a /g) ?? []).length).toBe(1);
    }
  });
  it("a row that begins like a numbered item past 1 is a row of the table, its link whole", () => {
    const html = renderWiki("| Step | Doc |\n|---|---|\n2. Build | [[arch|Architecture]]\n", () => ({ id: "page:/r#arch", broken: false, title: "Arch" }) as any);
    expect((html.match(/<a /g) ?? []).length).toBe(1);
    expect(html).toContain("Architecture");
    expect((html.match(/<tr/g) ?? []).length).toBe(2);
  });
});
