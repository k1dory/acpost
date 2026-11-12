// @vitest-environment happy-dom
// Review round 1 of the wiki's explorer: links whose label is code, destinations with parentheses,
// "Open" on a set newer than the reader's, and a large graph laid out off the render path.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
let mount: typeof import("svelte").mount, unmount: typeof import("svelte").unmount, flushSync: typeof import("svelte").flushSync;
import { fakeNode, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

let mounted: any[] = [];
beforeEach(async () => {
  vi.resetModules();
  fake = fakeNode();
  document.body.innerHTML = "";
  try { localStorage.clear(); } catch {}
  ({ mount, unmount, flushSync } = await import("svelte"));
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; });

// A body as the app shows it: linkified, then through the real Markdown component.
async function render(body: string, from: string, pages: any[]) {
  const { buildGraph, linkify } = await import("../lib/wikigraph");
  const g = buildGraph(pages, []);
  const Markdown = (await import("./Markdown.svelte")).default;
  mounted.push(mount(Markdown, { target: document.body, props: { text: linkify(body, g.byId.get(from)!, g) } }));
  flushSync();
  return g;
}

describe("wiki links (review round 1)", () => {
  it("a link whose label is code is a link, with its edge", async () => {
    const pages = [
      { repo: "/r", slug: "overview", title: "Overview", body: "The [[arch|`API`]] and `[[not-a-link]]`." },
      { repo: "/r", slug: "arch", title: "Architecture", body: "" },
    ];
    const g = await render(pages[0].body, "page:/r#overview", pages);
    const { linkTarget } = await import("../lib/wikigraph");
    const a = document.querySelector("a")!;
    expect(a.querySelector("code")?.textContent).toBe("API");
    expect(linkTarget(a.getAttribute("href")!)).toBe("page:/r#arch");
    expect(document.querySelectorAll("a").length).toBe(1); // the one in code stays code
    expect(g.edges).toContainEqual({ from: "page:/r#overview", to: "page:/r#arch", type: "link" });
    expect(g.broken).toEqual([]);
  });

  it("a destination with parentheses survives Markdown and names its page", async () => {
    const pages = [
      { repo: "/work/app)", slug: "overview", title: "Overview", body: "See [[arch]] and [[gone (old)]]." },
      { repo: "/work/app)", slug: "arch", title: "Architecture", body: "" },
    ];
    await render(pages[0].body, "page:/work/app)#overview", pages);
    const { linkTarget } = await import("../lib/wikigraph");
    const links = [...document.querySelectorAll("a")];
    expect(links.map(a => a.textContent)).toEqual(["Architecture", "gone (old)"]);
    expect(linkTarget(links[0].getAttribute("href")!)).toBe("page:/work/app)#arch");
    expect(decodeURIComponent(links[1].getAttribute("href")!.slice("#wiki-broken:".length))).toBe("gone (old)");
  });
});

describe("Open from the explorer (review round 1)", () => {
  async function wiki(wikis: any[], byRepo: Record<string, any[]>, all: any[]) {
    fake.Node.Wikis.mockResolvedValue(wikis);
    fake.Node.WikiPages.mockImplementation(async (r: string) => byRepo[r] ?? []);
    fake.Node.AllWikis.mockResolvedValue({ pages: all });
    fake.Node.Knowledge.mockResolvedValue([]);
    const Wiki = (await import("./Wiki.svelte")).default;
    mounted.push(mount(Wiki, { target: document.body }));
    await flush(); await flush(); flushSync();
  }
  const click = (el: Element | null | undefined) => { (el as HTMLElement).click(); flushSync(); };
  const seg = (label: string) => [...document.querySelectorAll(".seg button")].find(b => b.textContent === label);
  const row = (title: string) => [...document.querySelectorAll("tbody tr")].find(r => r.querySelector("td.tt")?.textContent === title);
  const openButton = () => [...document.querySelectorAll("aside.drawer button")].find(b => b.textContent === "Open");
  const stamp = "2026-11-12T10:00:00Z";

  it("opens a repository whose wiki was made after the reader loaded, at the page asked for", async () => {
    await wiki([{ repo: "/w/app", agent: "agent:claude", status: "ready", updated: stamp, pages: 1 }],
      { "/w/app": [{ slug: "overview", title: "Overview", body: "old" }] },
      [{ repo: "/w/new", slug: "intro", title: "Intro", body: "x", updated: stamp }, { repo: "/w/new", slug: "deep", title: "Deep", body: "new text", updated: stamp }]);
    click(seg("Table")); await flush(); flushSync();
    // Made elsewhere since: the node now lists it.
    fake.Node.Wikis.mockResolvedValue([
      { repo: "/w/app", agent: "agent:claude", status: "ready", updated: stamp, pages: 1 },
      { repo: "/w/new", agent: "agent:claude", status: "ready", updated: stamp, pages: 2 },
    ]);
    fake.Node.WikiPages.mockImplementation(async (r: string) => (r === "/w/new"
      ? [{ slug: "intro", title: "Intro", body: "x" }, { slug: "deep", title: "Deep", body: "new text" }]
      : [{ slug: "overview", title: "Overview", body: "old" }]));
    click(row("Deep"));
    click(openButton());
    await flush(); await flush(); flushSync();
    expect(document.querySelector("article.page h1")?.textContent).toBe("Deep");
    expect(document.querySelector("article.page")?.textContent).toContain("new text");
    expect(document.querySelector(".rhead b")?.textContent).toBe("new");
  });

  it("opens a repository written again with its new pages, not the reader's old ones", async () => {
    await wiki([{ repo: "/w/app", agent: "agent:claude", status: "ready", updated: stamp, pages: 1 }],
      { "/w/app": [{ slug: "overview", title: "Overview", body: "old text" }] },
      [{ repo: "/w/app", slug: "overview", title: "Overview", body: "new", updated: stamp }, { repo: "/w/app", slug: "added", title: "Added", body: "added text", updated: stamp }]);
    expect(document.querySelector("article.page")?.textContent).toContain("old text");
    click(seg("Table")); await flush(); flushSync();
    fake.Node.WikiPages.mockImplementation(async () => [{ slug: "overview", title: "Overview", body: "new" }, { slug: "added", title: "Added", body: "added text" }]);
    click(row("Added"));
    click(openButton());
    await flush(); await flush(); flushSync();
    expect(document.querySelector("article.page h1")?.textContent).toBe("Added");
    expect([...document.querySelectorAll("nav.tree .pg")].map(b => b.textContent)).toEqual(["Overview", "Added"]);
  });
});

describe("a large graph (review round 1)", () => {
  it("is laid out in slices: the window is told, then the nodes come", async () => {
    const ids = Array.from({ length: 2500 }, (_, i) => "page:/r#p" + i);
    const items = new Map(ids.map(id => [id, { id, kind: "page", title: id, key: id, body: "", updated: "" } as any]));
    const edges = ids.slice(1).map((id, i) => ({ from: ids[i >> 1], to: id, type: "link" as const }));
    const Graph = (await import("./WikiGraph.svelte")).default;
    mounted.push(mount(Graph, { target: document.body, props: { ids, items, edges, onselect: () => {} } }));
    flushSync();
    expect(document.querySelector(".laying")?.textContent).toContain("Laying out");
    expect(document.querySelectorAll("g.node").length).toBe(0);
    for (let i = 0; i < 2000 && document.querySelector(".laying"); i++) { await new Promise(r => setTimeout(r, 5)); flushSync(); }
    expect(document.querySelector(".laying")).toBeNull();
    expect(document.querySelectorAll("g.node").length).toBe(2500);
  }, 60000);
});
