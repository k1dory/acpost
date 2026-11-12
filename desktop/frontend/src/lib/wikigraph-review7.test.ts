import { describe, it, expect } from "vitest";
import { Marked } from "marked";
import { parseLinks, buildGraph, renderWiki, pickMask, wikiMarkdown, markdownOptions, pageId } from "./wikigraph";

// Review round 7: the table mask is a private-use character the body does not hold, chosen per
// parse and put back inside tables only: one the author wrote comes out as written and never
// separates a link's target from its label.
const P = "\uE000";
const table = "\n\n| a | b |\n|---|---|\n| [[real|R]] | y |";
const resolve = (t: string) => (t === "arch" || t === "real" ? { id: "page:/r#" + t, title: t.toUpperCase() } : null);
const doc = (html: string) => new DOMParser().parseFromString(html, "text/html");
const cells = (d: Document) => [...d.querySelectorAll("tbody tr")].map(tr => [...tr.children].map(c => c.textContent));

describe("a private-use character the author wrote", () => {
  const shapes: [string, string, (html: string, links: ReturnType<typeof parseLinks>) => void][] = [
    ["in prose", `a${P}b`, html => expect(html).toContain(`a${P}b`)],
    ["in a code span", `\`a${P}b\``, html => expect(html).toContain(`<code>a${P}b</code>`)],
    ["in a fence", "```\na" + P + "b\n```", html => expect(html).toContain(`a${P}b\n</code>`)],
    ["in a link target", `[[ar${P}ch]]`, (html, links) => {
      expect(links[0]).toEqual({ target: `ar${P}ch` });
      expect(doc(html).querySelector("a")?.textContent).toBe(`ar${P}ch`);
    }],
    ["in a label", `[[arch|x${P}y]]`, (html, links) => {
      expect(links[0]).toEqual({ target: "arch", label: `x${P}y` });
      expect(doc(html).querySelector("a")?.textContent).toBe(`x${P}y`);
    }],
    ["between target and label", `[[arch${P}label]]`, (html, links) => {
      expect(links[0]).toEqual({ target: `arch${P}label` }); // one target, no label: never a separator
      expect(doc(html).querySelector("a")?.getAttribute("href")).toMatch(/^#wiki-broken:/);
    }],
  ];
  for (const [name, body, check] of shapes) {
    for (const withTable of [false, true]) {
      it(`${name}${withTable ? ", with a table holding a labelled link" : ""}`, () => {
        const text = body + (withTable ? table : "");
        const html = renderWiki(text, resolve);
        const links = parseLinks(text);
        check(html, links);
        expect(html).not.toContain(`a|b`);
        expect(html.split(P).length).toBe(text.split(P).length); // as many as written, no more, no fewer
        expect(html).not.toContain(""); // the mask never shows
        if (withTable) {
          expect(links.at(-1)).toEqual({ target: "real", label: "R" });
          expect(cells(doc(html))).toEqual([["R", "y"]]);
          expect(pickMask(text)).toBe("\uE001");
        }
      });
    }
  }

  it("the graph has the link in the table and the literal's own target", () => {
    const body = `[[ar${P}ch]]` + table;
    const g = buildGraph([{ repo: "/r", slug: "p", title: "P", body }, { repo: "/r", slug: "real", title: "Real" }], []);
    expect(g.out.get(pageId("/r", "p"))!.map(e => e.to)).toEqual([pageId("/r", "real")]);
    expect(g.broken).toEqual([{ from: pageId("/r", "p"), target: `ar${P}ch` }]);
  });
});

describe("a mask per parse", () => {
  it("is chosen for each body by one instance, one after the other", () => {
    const md = new Marked(markdownOptions).use(wikiMarkdown(resolve));
    const first = `x${P}` + table;          // mask: U+E001
    const second = "y\uE001" + table;        // mask: U+E000 — and U+E001 is the author's
    for (const [text, kept, mask] of [[first, `x${P}`, "\uE001"], [second, "y\uE001", P], [first, `x${P}`, "\uE001"]] as const) {
      const html = md.parse(text, { async: false }) as string;
      expect(html).toContain(kept);
      expect(cells(doc(html))).toEqual([["R", "y"]]);
      expect(html).not.toContain(mask); // that parse's mask is put back; the other body's is not used
    }
    // The graph's parser too, body after body.
    expect(parseLinks(first).map(l => l.target)).toEqual(["real"]);
    expect(parseLinks(second).map(l => l.target)).toEqual(["real"]);
    expect(parseLinks(`[[arch\uE001b]]`)).toEqual([{ target: "arch\uE001b" }]);
  });

  it("is none when the body holds every private-use character: its table is marked's own", () => {
    let all = "";
    for (let c = 0xe000; c <= 0xf8ff; c++) all += String.fromCharCode(c);
    expect(pickMask(all)).toBe("");
    const html = renderWiki(all + table, resolve);
    expect(html).toContain(all);
  });
});
