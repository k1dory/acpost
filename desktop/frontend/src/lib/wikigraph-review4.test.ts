import { describe, it, expect } from "vitest";
import { parseLinks, buildGraph, renderWiki, pageId } from "./wikigraph";

// Review round 4: the wiki link is marked's own inline token, so wherever marked has code or HTML
// there is no link, and wherever it has text (cells, quoted lists, emphasis) there is.
const pages = (body: string) => [{ repo: "/r", slug: "p", title: "P", body }, { repo: "/r", slug: "arch", title: "Arch" }, { repo: "/r", slug: "real", title: "Real" }];
function check(body: string, want: string[]) {
  expect(parseLinks(body).map(l => l.target)).toEqual(want);
  const g = buildGraph(pages(body), []);
  const p = g.byId.get(pageId("/r", "p"))!;
  expect(g.out.get(p.id)!.map(e => e.to)).toEqual(want.map(s => pageId("/r", s)));
  const html = renderWiki(body, t => g.resolve(p, t));
  expect((html.match(/<a href="#wiki:/g) ?? []).length).toBe(want.length);
  return html;
}

describe("wiki links where marked has them", () => {
  it("not in a code span over two quoted lines", () => {
    const html = check("> before `multi\n> [[arch]]` after\n\n[[real]]", ["real"]);
    expect(html).toContain("[[arch]]</code>");
  });
  it("not in a code span with an escaped pipe in a table cell; yes in a cell", () => {
    check("| a | b |\n|---|---|\n| `x \\| [[arch]]` | [[real]] |", ["real"]);
  });
  it("in a list item in a quote", () => {
    check("> - one\n> - see [[real]]\n>   and `[[arch]]`", ["real"]);
  });
  it("in emphasis, and with a code label in strong", () => {
    const html = check("*see [[real]]* and **[[arch|`API`]]**", ["real", "arch"]);
    expect(html).toContain("<strong><a href=\"#wiki:");
    expect(html).toContain("<code>API</code></a>");
  });
  it("not in an HTML block or a tag's attribute", () => {
    check("<div>\n[[arch]]\n</div>\n\n[[real]]", ["real"]);
    check("<span title=\"[[arch]]\">x</span> [[real]]", ["real"]);
  });
  it("earlier shapes still: fences, quoted fences, inline code, CRLF", () => {
    check("```\n[[arch]]\n```\n> ```\n> [[arch]]\n> ```\n\n`[[arch]]` [[real]]", ["real"]);
    check("~~~\r\n[[arch]]\r\n~~~\r\n[[real]]", ["real"]);
  });
});
