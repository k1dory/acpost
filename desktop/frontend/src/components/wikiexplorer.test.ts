// @vitest-environment happy-dom
// The wiki's explorer mounted for real (happy-dom), the node faked: what it loads, how the table
// sorts and filters, the drawer, the graph, and the page view's links.
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

const pages = [
  { repo: "/w/app", slug: "overview", title: "Overview", body: "Start at [[arch]]; see [[style]] and [[gone]].", updated: "2026-11-10T10:00:00Z" },
  { repo: "/w/app", slug: "arch", title: "Architecture", body: "The <b>parts</b>.", updated: "2026-11-10T10:00:00Z" },
  { repo: "/w/app", slug: "store", title: "Storage", parent: "arch", body: "SQLite", updated: "2026-11-10T10:00:00Z" },
  { repo: "/w/lib", slug: "lonely", title: "Lonely page", body: "nothing here", updated: "2026-11-01T10:00:00Z" },
];
const notes = [{ name: "style", body: "Write **short**.", enabled: true, updated: "2026-11-11T10:00:00Z" }];

let mounted: any[] = [];
beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.AllWikis.mockResolvedValue({ pages });
  fake.Node.Knowledge.mockResolvedValue(notes);
  fake.Node.Wikis.mockResolvedValue([{ repo: "/w/app", agent: "agent:claude", status: "ready", updated: "2026-11-10T10:00:00Z", pages: 3 }]);
  fake.Node.WikiPages.mockResolvedValue(pages.slice(0, 3));
  document.body.innerHTML = "";
  try { localStorage.clear(); } catch {}
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; });

async function explorer(mode: "table" | "graph", onopen = vi.fn()) {
  ({ mount, unmount, flushSync } = await import("svelte"));
  const Explorer = (await import("./WikiExplorer.svelte")).default;
  // selected is bound: the component sets it through the accessor.
  const sel = (await import("../test/box.svelte")).box("");
  const props = { mode, onopen, get selected() { return sel.v; }, set selected(v: string) { sel.v = v; } };
  mounted.push(mount(Explorer, { target: document.body, props }));
  await flush(); flushSync();
  return props;
}
const titles = () => [...document.querySelectorAll("tbody td.tt")].map(td => td.textContent);
const input = (el: HTMLInputElement, v: string) => { el.value = v; el.dispatchEvent(new Event("input", { bubbles: true })); flushSync(); };
const button = (text: string) => [...document.querySelectorAll("button")].find(b => b.textContent?.trim() === text) as HTMLButtonElement;

describe("the wiki's explorer", () => {
  it("loads every wiki and the notes, as a table that sorts, filters by kind and searches the text", async () => {
    await explorer("table");
    expect(fake.Node.AllWikis).toHaveBeenCalledTimes(1);
    expect(titles()).toEqual(["style", "Architecture", "Overview", "Storage", "Lonely page"]);
    // Out/in counts: the overview links to arch and style (its [[gone]] is broken).
    const row = [...document.querySelectorAll("tbody tr")].find(r => r.textContent?.includes("Overview"))!;
    expect([...row.querySelectorAll("td.num")].map(td => td.textContent)).toEqual(["2", "0"]);
    button("Title").click(); flushSync();
    expect(titles()).toEqual(["Architecture", "Lonely page", "Overview", "Storage", "style"]);
    (document.querySelector("th[aria-sort='ascending'] button") as HTMLElement).click(); flushSync();
    expect(titles()[0]).toBe("style");
    button("Notes").click(); flushSync();
    expect(titles()).toEqual(["style"]);
    button("All").click(); flushSync();
    input(document.querySelector(".search input") as HTMLInputElement, "sqlite");
    expect(titles()).toEqual(["Storage"]);
    input(document.querySelector(".search input") as HTMLInputElement, "zzz");
    expect(document.querySelector("tbody")!.textContent).toContain("Nothing matches");
  });

  it("opens an item in a drawer: its Markdown, links out with the broken one, backlinks, and links that move it", async () => {
    const onopen = vi.fn();
    const props = await explorer("table", onopen);
    ([...document.querySelectorAll("tbody tr")].find(r => r.textContent?.includes("Overview")) as HTMLElement).click();
    flushSync();
    expect(props.selected).toBe("page:/w/app#overview");
    const drawer = document.querySelector("aside.drawer")!;
    expect(drawer.querySelector("a[href^='#wiki-broken:']")?.textContent).toBe("gone");
    expect(drawer.textContent).toContain("broken");
    expect(drawer.querySelector(".ng svg")).not.toBeNull(); // the neighbour graph
    (drawer.querySelector("article a[href^='#wiki:note']") as HTMLElement).click();
    flushSync();
    expect(props.selected).toBe("note:style");
    const note = document.querySelector("aside.drawer")!;
    expect(note.querySelector("article strong")?.textContent).toBe("short"); // Markdown, through the app's renderer
    expect(note.textContent).toContain("Overview"); // a backlink
    button("Open").click();
    expect(onopen).toHaveBeenCalledWith(expect.objectContaining({ id: "note:style", kind: "note" }));
    window.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape" })); flushSync();
    expect(document.querySelector("aside.drawer")).toBeNull();
  });

  it("draws the graph with every item, lists those on their own and the broken links, and refreshes", async () => {
    const props = await explorer("graph");
    const nodes = document.querySelectorAll(".canvas g.node");
    expect(nodes.length).toBe(5);
    expect(document.querySelectorAll(".canvas line.edge").length).toBe(3); // overview→arch, overview→style, store part of arch
    expect(document.querySelectorAll(".canvas line.edge.part").length).toBe(1);
    const lists = document.querySelector(".lists")!.textContent!;
    expect(lists).toContain("Lonely page");
    expect(lists).toContain("gone");
    // A press without a move on a node selects it.
    const n = [...nodes].find(g => g.getAttribute("aria-label") === "Storage")!;
    n.dispatchEvent(new PointerEvent("pointerdown", { button: 0, bubbles: true, clientX: 5, clientY: 5 }));
    document.querySelector(".canvas svg")!.dispatchEvent(new PointerEvent("pointerup", { bubbles: true, clientX: 5, clientY: 5 }));
    flushSync();
    expect(props.selected).toBe("page:/w/app#store");
    // A search fades what does not match; the layout stays.
    const before = n.getAttribute("transform");
    input(document.querySelector(".search input") as HTMLInputElement, "overview");
    expect(document.querySelectorAll(".canvas g.node.faint").length).toBeGreaterThan(0);
    expect([...document.querySelectorAll(".canvas g.node")].find(g => g.getAttribute("aria-label") === "Storage")!.getAttribute("transform")).toBe(before);
    fake.Node.AllWikis.mockResolvedValue({ pages: pages.slice(0, 1), more: true });
    button("Refresh").click();
    await flush(); flushSync();
    expect(fake.Node.AllWikis).toHaveBeenCalledTimes(2);
    expect(document.body.textContent).toContain("Not every page is loaded");
    expect(props.selected).toBe(""); // the selected page is gone
  });

  it("says what failed when the node does not answer", async () => {
    fake.Node.AllWikis.mockRejectedValue(new Error("node down"));
    await explorer("table");
    expect(document.querySelector(".err")?.textContent).toContain("node down");
  });
});

describe("the wiki's page view", () => {
  it("makes [[links]] links: to a page of the wiki it turns the page; to a note it opens the explorer; a broken one goes nowhere", async () => {
    ({ mount, unmount, flushSync } = await import("svelte"));
    const Wiki = (await import("./Wiki.svelte")).default;
    mounted.push(mount(Wiki, { target: document.body }));
    await flush(); await flush(); flushSync();
    const article = () => document.querySelector("article.page")!;
    expect(article().querySelector("h1")?.textContent).toBe("Overview");
    expect(article().querySelector("a[href^='#wiki-broken:']")?.textContent).toBe("gone");
    (article().querySelector("a[href*='arch']") as HTMLElement).click(); flushSync();
    expect(article().querySelector("h1")?.textContent).toBe("Architecture");
    (document.querySelectorAll("nav.tree button")[0] as HTMLElement).click(); flushSync();
    (article().querySelector("a[href^='#wiki:note']") as HTMLElement).click();
    await flush(); flushSync();
    expect(document.querySelector("aside.drawer")?.textContent).toContain("style");
    expect(document.querySelector(".seg button.on")?.textContent).toBe("Table");
  });
});
