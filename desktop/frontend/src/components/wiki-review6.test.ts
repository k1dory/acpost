// @vitest-environment happy-dom
// Review round 6: a label is rendered by the wiki link itself, escaped from its own source, so
// HTML that marked would pass through as it is (inside <code>…</code>) cannot add a tag.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
let mount: typeof import("svelte").mount, unmount: typeof import("svelte").unmount, flushSync: typeof import("svelte").flushSync;

vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

let mounted: any[] = [];
beforeEach(async () => {
  vi.resetModules();
  document.body.innerHTML = "";
  ({ mount, unmount, flushSync } = await import("svelte"));
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; });

const href = "#wiki:page%3A%2Fr%23arch";
async function render(text: string) {
  const Markdown = (await import("./Markdown.svelte")).default;
  mounted.push(mount(Markdown, { target: document.body, props: { text, wikiLink: (t: string) => (t === "arch" ? { id: "page:/r#arch", title: "Arch" } : null) } }));
  flushSync();
  return document.querySelector(".md")!;
}

describe("a label with HTML in it", () => {
  for (const [name, text] of [
    ["an anchor in <code> HTML", "[[arch|<code><a/href=//evil.example>click</a></code>]]"],
    ["a picture in <code> HTML", "[[arch|<code><img src=x onerror=alert(1)></code>]]"],
    ["HTML nested in emphasis", "[[arch|*<b><a href=//evil.example>x</a></b>* **<code><i>y</i></code>**]]"],
  ]) {
    it(`is text: ${name}`, async () => {
      const md = await render(text);
      const anchors = [...md.querySelectorAll("a")];
      expect(anchors.map(a => a.getAttribute("href"))).toEqual([href]);
      expect(md.querySelector("img")).toBeNull();
      for (const el of anchors[0].querySelectorAll("*")) {
        expect(["CODE", "EM", "STRONG", "DEL"]).toContain(el.tagName);
        expect(el.attributes.length).toBe(0);
      }
      expect(anchors[0].textContent).toContain("<");
    });
  }
});
