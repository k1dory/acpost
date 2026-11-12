import { describe, it, expect } from "vitest";
import { Lexer } from "marked";
import { parseLinks, buildGraph, linkify, codeRanges, pageId } from "./wikigraph";

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
      const md = linkify(body, g.byId.get(pageId("/r", "p"))!, g);
      expect(md.split("[[arch]]").length - 1).toBe(body.split("[[arch]]").length - 1 - (expected[0] === "arch" ? 1 : 0));
    });
  }

  it("covers every code block's lines, and top-level raws are the text in order", () => {
    const body = `${quoted}\n\n${block}`;
    const r = codeRanges(body);
    const covered = (needle: string, from: number) => { const i = body.indexOf(needle, from); return r.some(([a, b]) => i >= a && i < b); };
    expect(covered("[[arch]]", 0)).toBe(true);
    expect(covered("[[arch]]", body.indexOf("[[arch]]") + 1)).toBe(true);
    for (const s of [body, `${listed}\n\n${block}`, "a\r\nb"]) {
      const text = s.replace(/\r\n?/g, "\n");
      expect(new Lexer({ gfm: true, breaks: true }).lex(text).map(t => t.raw).join("")).toBe(text);
    }
  });
});
