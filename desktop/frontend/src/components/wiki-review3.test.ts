// @vitest-environment happy-dom
// Review round 3 of the wiki's explorer: an older list of wikis (answered or failed after the one
// Open asked for) does not undo Open.
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
const wiki = (repo: string) => ({ repo, agent: "agent:claude", status: "ready", updated: stamp, pages: 1 });
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

describe("the reader's list of wikis (review round 3)", () => {
  for (const how of ["answers", "fails"] as const) {
    it(`an older list that ${how} after Open's does not take the opened page away`, async () => {
      // The first load's list is held back; the explorer shows a wiki made since.
      const first = deferred<any>();
      fake.Node.Wikis.mockImplementationOnce(() => first.promise).mockResolvedValue([wiki("/w/app"), wiki("/w/new")]);
      fake.Node.WikiPages.mockImplementation(async (r: string) => (r === "/w/new" ? [{ slug: "deep", title: "Deep", body: "new text" }] : []));
      fake.Node.AllWikis.mockResolvedValue({ pages: [{ repo: "/w/new", slug: "deep", title: "Deep", body: "new text", updated: stamp }] });
      fake.Node.Knowledge.mockResolvedValue([]);
      const Wiki = (await import("./Wiki.svelte")).default;
      mounted.push(mount(Wiki, { target: document.body }));
      await settle();
      click([...document.querySelectorAll(".seg button")].find(b => b.textContent === "Table"));
      await settle();
      click([...document.querySelectorAll("tbody tr")].find(r => r.textContent?.includes("Deep")));
      click([...document.querySelectorAll("aside.drawer button")].find(b => b.textContent === "Open"));
      await settle();
      expect(document.querySelector("article.page h1")?.textContent).toBe("Deep");
      // The first load's answer comes last: a list without the opened wiki, or a failure.
      if (how === "answers") first.resolve([wiki("/w/app")]); else first.reject(new Error("old failure"));
      await settle();
      expect(document.querySelector("article.page h1")?.textContent).toBe("Deep");
      expect(document.querySelector(".rhead b")?.textContent).toBe("new");
      expect(document.querySelector(".err")).toBeNull();
    });
  }
});
