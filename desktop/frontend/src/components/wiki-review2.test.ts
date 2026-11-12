// @vitest-environment happy-dom
// Review round 2 of the wiki's explorer: the reader takes only its latest answer for pages, and
// "Open" reads the notes again, which a page's links may name.
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
const wikis = [{ repo: "/w/app", agent: "agent:claude", status: "ready", updated: stamp, pages: 2 }];
let mounted: any[] = [];
beforeEach(async () => {
  vi.resetModules();
  fake = fakeNode();
  document.body.innerHTML = "";
  try { localStorage.clear(); } catch {}
  ({ mount, unmount, flushSync } = await import("svelte"));
  fake.Node.Wikis.mockResolvedValue(wikis);
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; });

const settle = async () => { await flush(); await flush(); flushSync(); };
const click = (el: Element | null | undefined) => { (el as HTMLElement).click(); flushSync(); };
const seg = (label: string) => [...document.querySelectorAll(".seg button")].find(b => b.textContent === label);
const row = (title: string) => [...document.querySelectorAll("tbody tr")].find(r => r.querySelector("td.tt")?.textContent === title);
const openButton = () => [...document.querySelectorAll("aside.drawer button")].find(b => b.textContent === "Open");
const article = () => document.querySelector("article.page");
async function mountWiki() {
  const Wiki = (await import("./Wiki.svelte")).default;
  mounted.push(mount(Wiki, { target: document.body }));
  await settle();
}
// The explorer's table, then Open on a row's drawer.
async function openFromTable(title: string) {
  click(seg("Table")); await settle();
  click(row(title));
  click(openButton());
  await settle();
}

describe("the reader's pages (review round 2)", () => {
  const fresh = [{ slug: "overview", title: "Overview", body: "new text" }, { slug: "added", title: "Added", body: "added text" }];
  const stale = [{ slug: "overview", title: "Overview", body: "old text" }];
  for (const how of ["answers", "fails"] as const) {
    it(`an older request for the same repository that ${how} after Open's does not replace its pages`, async () => {
      const old = deferred<any>(), now = deferred<any>();
      fake.Node.WikiPages.mockImplementationOnce(() => old.promise).mockImplementationOnce(() => now.promise);
      fake.Node.AllWikis.mockResolvedValue({ pages: fresh.map(p => ({ ...p, repo: "/w/app", updated: stamp })) });
      fake.Node.Knowledge.mockResolvedValue([]);
      await mountWiki();
      await openFromTable("Added");
      now.resolve(fresh); await settle();
      expect(article()?.querySelector("h1")?.textContent).toBe("Added");
      if (how === "answers") old.resolve(stale); else old.reject(new Error("late failure"));
      await settle();
      expect(article()?.querySelector("h1")?.textContent).toBe("Added");
      expect([...document.querySelectorAll("nav.tree .pg")].map(b => b.textContent)).toEqual(["Overview", "Added"]);
      expect(document.querySelector(".err")).toBeNull();
    });
  }
});

describe("the reader's notes (review round 2)", () => {
  const page = { slug: "overview", title: "Overview", body: "See [[style]]." };
  beforeEach(() => {
    fake.Node.WikiPages.mockResolvedValue([page]);
    fake.Node.AllWikis.mockResolvedValue({ pages: [{ ...page, repo: "/w/app", updated: stamp }] });
  });
  const linkKind = () => {
    const h = decodeURIComponent(article()?.querySelector("a")?.getAttribute("href") ?? "");
    return h.startsWith("#wiki:note:") ? "note" : h.startsWith("#wiki-broken:") ? "broken" : "none";
  };

  it("a note made after the reader loaded resolves once the page is opened", async () => {
    fake.Node.Knowledge.mockResolvedValue([]);
    await mountWiki();
    expect(linkKind()).toBe("broken");
    fake.Node.Knowledge.mockResolvedValue([{ name: "style", body: "short", enabled: true, updated: stamp }]);
    await openFromTable("Overview");
    expect(linkKind()).toBe("note");
  });

  it("a note deleted after the reader loaded is broken once the page is opened", async () => {
    fake.Node.Knowledge.mockResolvedValue([{ name: "style", body: "short", enabled: true, updated: stamp }]);
    await mountWiki();
    expect(linkKind()).toBe("note");
    fake.Node.Knowledge.mockResolvedValue([]);
    await openFromTable("Overview");
    expect(linkKind()).toBe("broken");
  });
});
