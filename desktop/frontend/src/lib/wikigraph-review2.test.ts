import { describe, it, expect } from "vitest";
import { parseLinks, buildGraph, renderWiki, pageId } from "./wikigraph";

// Review round 2: code is where Markdown (marked, the app's renderer) says it is.
describe("wiki links and Markdown's code", () => {
  const targets = (s: string) => parseLinks(s).map(l => l.target);

  it("a line with an inline span of three backticks opens no fence", () => {
    const body = "```x``` at the start, [[a]]\n\nthen [[b]]\n\nuse ```x``` here, `[[not]]`, ```[[not]]``` too, [[c]]";
    expect(targets(body)).toEqual(["a", "b", "c"]);
    const g = buildGraph([{ repo: "/r", slug: "p", title: "P", body }, ...["a", "b", "c"].map(s => ({ repo: "/r", slug: s, title: s.toUpperCase() }))], []);
    expect(g.out.get(pageId("/r", "p"))!.map(e => e.to)).toEqual(["a", "b", "c"].map(s => pageId("/r", s)));
    const p = g.byId.get(pageId("/r", "p"))!;
    const html = renderWiki(body, t => g.resolve(p, t));
    expect(html).toContain("<code>[[not]]</code>");
    expect(html.match(/#wiki:/g)).toHaveLength(3);
  });

  it("follows fences: tilde fences, a backtick fence's info has no backtick, a closing fence as long", () => {
    expect(targets("~~~\n[[no]]\n~~~\n[[yes]]")).toEqual(["yes"]);
    expect(targets("~~~~\n[[no]]\n~~~\n[[no]]\n~~~~\n[[yes]]")).toEqual(["yes"]);
    expect(targets("```\n[[no]]\n```\n[[yes]]")).toEqual(["yes"]);
    expect(targets("````\n```\n[[no]]\n````\n[[yes]]")).toEqual(["yes"]);
    // A "fence" with a backtick in its info string is inline code, not a block.
    expect(targets("``` a`b\n[[yes]]")).toEqual(["yes"]);
    // Four spaces in: an indented code block, not a fence.
    expect(targets("para\n\n    [[no]]\n\n[[yes]]")).toEqual(["yes"]);
  });

  it("an unclosed fence runs to the end", () => {
    expect(targets("[[yes]]\n\n```\n[[no]]\n[[no2]]")).toEqual(["yes"]);
    expect(targets("[[yes]]\n~~~\n[[no]]")).toEqual(["yes"]);
  });

  it("finds code in quotes and lists, and keeps \\r\\n text", () => {
    expect(targets("> ```\n> [[no]]\n> ```\n\n[[yes]]")).toEqual(["yes"]);
    expect(targets("- item `[[no]]` [[yes]]\n- [[yes2]]")).toEqual(["yes", "yes2"]);
    expect(targets("```\r\n[[no]]\r\n```\r\n[[yes]]")).toEqual(["yes"]);
  });
});
