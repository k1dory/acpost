// @vitest-environment happy-dom
// Review round 5: a wiki link's label holds no link or markup of its own — through the real
// Markdown component the label is text and the one anchor is the wiki link's.
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

describe("a wiki link's label", () => {
  const cases: [string, string, string][] = [
    ["a web address", "[[arch|https://example.com]]", "https://example.com"],
    ["a bare domain", "[[arch|www.example.com]]", "www.example.com"],
    ["an e-mail", "[[arch|me@example.com]]", "me@example.com"],
    ["an <a href>", "[[arch|<a href=\"https://evil.example\">x</a>]]", "<a href=\"https://evil.example\">x</a>"],
    ["an <img onerror>", "[[arch|<img src=x onerror=alert(1)>]]", "<img src=x onerror=alert(1)>"],
    ["a Markdown link", "[[arch|see [x](https://evil.example)]]", "see [x](https://evil.example)"],
    ["an autolink in emphasis", "[[arch|*go <https://evil.example>*]]", "go <https://evil.example>"],
  ];
  for (const [name, text, shown] of cases) {
    it(`is text when it is ${name}`, async () => {
      const md = await render(text);
      const anchors = [...md.querySelectorAll("a")];
      expect(anchors.map(a => a.getAttribute("href"))).toEqual([href]);
      expect(md.querySelector("img")).toBeNull();
      if (shown) expect(anchors[0].textContent).toBe(shown);
    });
  }

  it("keeps code, emphasis and strike-through", async () => {
    const md = await render("[[arch|`API` *and* **more** ~~old~~]]");
    const a = md.querySelector("a")!;
    expect(a.getAttribute("href")).toBe(href);
    expect([...a.children].map(c => c.tagName)).toEqual(["CODE", "EM", "STRONG", "DEL"]);
  });
});
