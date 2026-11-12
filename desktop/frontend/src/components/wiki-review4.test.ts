// @vitest-environment happy-dom
// Review round 4 of the wiki's explorer: a table left (the view switched) and its answer coming
// late does not close the drawer the next table opened.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
let mount: typeof import("svelte").mount, unmount: typeof import("svelte").unmount, flushSync: typeof import("svelte").flushSync;
import { fakeNode, deferred, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

const stamp = "2026-11-12T10:00:00Z";
let mounted: any[] = [];
beforeEach(async () => {
  vi.resetModules();
  fake = fakeNode();
  document.body.innerHTML = "";
  try { localStorage.clear(); } catch {}
  ({ mount, unmount, flushSync } = await import("svelte"));
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; });

const settle = async () => { await flush(); await flush(); flushSync(); };
const click = (el: Element | null | undefined) => { (el as HTMLElement).click(); flushSync(); };
const seg = (label: string) => [...document.querySelectorAll(".seg button")].find(b => b.textContent === label);

describe("a table left while it loads (review round 4)", () => {
  it("its late answer does not clear what the next table selected", async () => {
    const first = deferred<any>(), second = deferred<any>();
    fake.Node.Wikis.mockResolvedValue([]);
    fake.Node.Knowledge.mockResolvedValue([]);
    fake.Node.AllWikis.mockImplementationOnce(() => first.promise).mockImplementationOnce(() => second.promise);
    const Wiki = (await import("./Wiki.svelte")).default;
    mounted.push(mount(Wiki, { target: document.body }));
    await settle();
    click(seg("Table")); await settle();   // the first table: its load is slow
    click(seg("Pages")); await settle();   // left
    click(seg("Table")); await settle();   // a new table
    second.resolve({ pages: [{ repo: "/w/app", slug: "new", title: "Newer", body: "x", updated: stamp }] });
    await settle();
    click([...document.querySelectorAll("tbody tr")].find(r => r.textContent?.includes("Newer")));
    expect(document.querySelector("aside.drawer")?.textContent).toContain("Newer");
    first.resolve({ pages: [{ repo: "/w/app", slug: "old", title: "Older", body: "x", updated: stamp }] });
    await settle();
    expect(document.querySelector("aside.drawer")?.textContent).toContain("Newer");
    expect([...document.querySelectorAll("tbody td.tt")].map(td => td.textContent)).toEqual(["Newer"]);
  });
});

describe("the reader's Markdown (review round 4)", () => {
  it("renders wiki links with marked: none in a quoted two-line code span or an HTML block, one in a quoted list's emphasis", async () => {
    const Markdown = (await import("./Markdown.svelte")).default;
    const text = "> before `multi\n> [[arch]]` after\n\n<div>\n[[arch]]\n</div>\n\n> - *see [[arch|`API`]]*";
    mounted.push(mount(Markdown, { target: document.body, props: { text, wikiLink: (t: string) => (t === "arch" ? { id: "page:/r#arch", title: "Arch" } : null) } }));
    flushSync();
    const links = [...document.querySelectorAll(".md a")];
    expect(links.map(a => a.getAttribute("href"))).toEqual(["#wiki:page%3A%2Fr%23arch"]);
    expect(links[0].closest("em")).not.toBeNull();
    expect(links[0].querySelector("code")?.textContent).toBe("API");
    expect(document.querySelector(".md code")?.textContent).toContain("[[arch]]");
  });
});
