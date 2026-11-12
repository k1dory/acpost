import { describe, it, expect } from "vitest";
import { parseLinks, buildGraph, renderWiki, pageId } from "./wikigraph";

// Review round 3: a code token is matched to its own place in the text, not to an identical one
// elsewhere — the same fenced block quoted, in a list item, and at the top, in either order.
const block = "```\n[[arch]]\n```";
const quoted = "> ```\n> [[arch]]\n> ```";
const listed = "- item\n\n  ```\n  [[arch]]\n  ```";
const targets = (s: string) => parseLinks(s).map(l => l.target);

describe("code ranges, each in its own place", () => {
  const cases: [string, string][] = [
    ["quote then top", `${quoted}\n\n${block}\n\nafter [[real]]`],
    ["top then quote", `${block}\n\n${quoted}\n\nafter [[real]]`],
    ["list then top", `${listed}\n\n${block}\n\nafter [[real]]`],
    ["top then list", `${block}\n\n${listed}\n\nafter [[real]]`],
    ["quote then list", `${quoted}\n\n${listed}\n\nafter [[real]]`],
    ["two in one quote", `${quoted}\n>\n${quoted}\n\nafter [[real]]`],
    ["text like the code before the quote", `[[arch]] here\n\n${quoted}\n\n${block}`],
  ];
  for (const [name, body] of cases) {
    it(name, () => {
      const expected = name.startsWith("text like") ? ["arch"] : ["real"];
      expect(targets(body)).toEqual(expected);
      const g = buildGraph([{ repo: "/r", slug: "p", title: "P", body }, { repo: "/r", slug: "arch", title: "Arch" }, { repo: "/r", slug: "real", title: "Real" }], []);
      expect(g.out.get(pageId("/r", "p"))!.map(e => e.to)).toEqual(expected.map(s => pageId("/r", s)));
      // Every [[arch]] in code stays as written.
      const p = g.byId.get(pageId("/r", "p"))!;
      const md = renderWiki(body, t => g.resolve(p, t));
      expect(md.split("[[arch]]").length - 1).toBe(body.split("[[arch]]").length - 1 - (expected[0] === "arch" ? 1 : 0));
    });
  }

  it("renders both identical blocks as code, and no link for either", () => {
    const body = `${quoted}\n\n${block}`;
    const html = renderWiki(body, () => ({ id: "x", title: "X" }));
    expect(html.match(/<code>\[\[arch\]\]/g)).toHaveLength(2);
    expect(html).not.toContain("#wiki");
  });
});
