// @vitest-environment happy-dom
// The library's review round 1: a reload and its pages, an old chat opened, links read from the
// artifact's own directory, previews asked again.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
let mount: typeof import("svelte").mount, unmount: typeof import("svelte").unmount, flushSync: typeof import("svelte").flushSync;
import { fakeNode, deferred, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

class FakeIO {
  static all: FakeIO[] = [];
  el: Element | null = null;
  constructor(public cb: (es: { isIntersecting: boolean }[]) => void) { FakeIO.all.push(this); }
  observe(el: Element) { this.el = el; }
  disconnect() {}
  show(v: boolean) { this.cb([{ isIntersecting: v }]); flushSync(); }
}
const ioOf = (el: Element) => FakeIO.all.filter(o => o.el === el).pop()!;

const now = new Date().toISOString();
const it_ = (n: number, o: any = {}) => ({ kind: "file", name: `f${n}.md`, type: "text/markdown", size: 5, msg: `m${n}`, thread: `T${n}`, title: `chat ${n}`, agent: "agent:claude", at: now, ...o });

let mounted: any[] = [];
beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  FakeIO.all = [];
  vi.stubGlobal("IntersectionObserver", FakeIO);
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Thread.mockResolvedValue([]);
  document.body.innerHTML = "";
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; vi.unstubAllGlobals(); });

async function show(what: "Library" | "App" = "Library") {
  ({ mount, unmount, flushSync } = await import("svelte"));
  const { app } = await import("../lib/state.svelte");
  app.conn = { ok: true } as any;
  const C = what === "App" ? (await import("../App.svelte")).default : (await import("./Library.svelte")).default;
  mounted.push(mount(C, { target: document.body }));
  flushSync();
  await flush(); flushSync();
  return app;
}
const settle = async () => { await flush(); flushSync(); await flush(); flushSync(); };
const refreshButton = () => document.querySelector('header button[aria-label="Refresh"]') as HTMLButtonElement;

describe("the library, review round 1", () => {
  it("does not ask for a page after the old cursor while a reload's first page is coming", async () => {
    const reload = deferred<any>();
    fake.Node.Artifacts
      .mockResolvedValueOnce({ items: [it_(1)], next: "c1" })
      .mockResolvedValueOnce({ items: [it_(2)], next: "c2" })
      .mockReturnValueOnce(reload.promise)
      .mockResolvedValueOnce({ items: [it_(9)], next: "" });
    await show();
    ioOf(document.querySelector(".tail")!).show(true);
    await settle();
    expect(fake.Node.Artifacts).toHaveBeenLastCalledWith("", "c1", 60);
    const oldTail = ioOf(document.querySelector(".tail")!);

    refreshButton().click();
    flushSync();
    expect(fake.Node.Artifacts).toHaveBeenLastCalledWith("", "", 60);
    oldTail.show(true); // the end of the old list still on screen
    await settle();
    expect(fake.Node.Artifacts).toHaveBeenCalledTimes(3); // no page after c2

    reload.resolve({ items: [it_(1), it_(2)], next: "n1" });
    await settle();
    ioOf(document.querySelector(".tail")!).show(true);
    await settle();
    expect(fake.Node.Artifacts).toHaveBeenLastCalledWith("", "n1", 60);
    expect([...document.querySelectorAll(".lc .nm")].map(e => e.textContent)).toEqual(["f1.md", "f2.md", "f9.md"]);
  });

  it("opens the chat of an artifact older than the chats listed", async () => {
    fake.Node.Artifacts.mockResolvedValue({ items: [it_(7, { cwd: "/p7" })], next: "" });
    const app = await show();
    app.chats = [{ key: "thread:OTHER", kind: "thread", thread: "OTHER", title: "selected", updated: now }] as any;
    fake.Node.Chats.mockResolvedValue({ items: [{ key: "thread:OTHER", kind: "thread", thread: "OTHER", title: "selected", updated: now }] });
    (document.querySelector(".lc .chat") as HTMLButtonElement).click();
    await settle();
    expect(app.view).toBe("chat");
    expect(app.chat?.key).toBe("thread:T7");
    expect(app.chat?.title).toBe("chat 7");
    expect(fake.Node.Thread).toHaveBeenCalledWith("T7");
  });

  it("reads a document's relative links from its own directory, not the open chat's", async () => {
    fake.Node.Status.mockResolvedValue({ ok: true });
    fake.Node.Home.mockResolvedValue("/Users/me");
    fake.Node.Agents.mockResolvedValue([]);
    fake.Node.Artifacts.mockResolvedValue({ items: [{ kind: "change", name: "report.md", type: "text/markdown", size: 30, path: "/project-b/report.md", msg: "m2", thread: "T2", at: now }], next: "" });
    fake.Node.LocalFile.mockImplementation(async (path: string, base: string) =>
      ({ name: path.split("/").pop(), type: "text/markdown", size: 30, path, data: btoa("See [the data](data/table.csv) and `notes/more.md`.") }));
    const app = await show("App");
    // A chat in another project is the one selected.
    app.msgs = [{ id: "a1", from: "user:me", to: "agent:claude", thread: "a1", depth: 0, state: "done", created_at: "", updated_at: "", meta: { acp_cwd: "/project-a" } }] as any;
    app.view = "artifacts";
    await settle();
    (document.querySelector(".lc .hit") as HTMLButtonElement).click();
    await settle();
    const link = document.querySelector(".av .md-wrap a") as HTMLAnchorElement;
    link.dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    flushSync();
    expect(app.artifact).toEqual({ name: "table.csv", path: "data/table.csv", base: "/project-b" });
    app.view = "artifacts";
    await settle();
    (document.querySelector(".lc .hit") as HTMLButtonElement).click();
    await settle();
    (document.querySelector(".av .md-wrap code.pl") as HTMLElement).dispatchEvent(new MouseEvent("click", { bubbles: true, cancelable: true }));
    flushSync();
    expect(app.artifact).toEqual({ name: "more.md", path: "notes/more.md", base: "/project-b" });
  });

  it("asks for previews again on refresh: a changed file anew, a failed one again", async () => {
    fake.Node.Artifacts.mockResolvedValue({ items: [it_(1), it_(2)], next: "" });
    fake.Node.ArtifactPreview
      .mockImplementationOnce(async () => ({ type: "text/markdown", data: btoa("old text") }))
      .mockImplementationOnce(async () => { throw new Error("busy"); })
      .mockImplementation(async (_m: string, name: string) => ({ type: "text/markdown", data: btoa("new " + name) }));
    await show();
    for (const c of document.querySelectorAll(".lc")) ioOf(c).show(true);
    await settle();
    expect(fake.Node.ArtifactPreview).toHaveBeenCalledTimes(2);
    const texts = () => [...document.querySelectorAll(".lc")].map(c => c.querySelector("pre")?.textContent ?? "");
    expect(texts()).toEqual(["old text", ""]);

    refreshButton().click();
    await settle();
    expect(fake.Node.ArtifactPreview).toHaveBeenCalledTimes(4);
    expect(texts()).toEqual(["new f1.md", "new f2.md"]);

    // A list that comes back with the file changed (its size, its time) asks again without a refresh.
    fake.Node.Artifacts.mockResolvedValue({ items: [it_(1, { size: 99 }), it_(2)], next: "" });
    const input = document.querySelector(".filter input") as HTMLInputElement;
    input.value = "f"; input.dispatchEvent(new Event("input", { bubbles: true })); flushSync();
    await new Promise(r => setTimeout(r, 300));
    await settle();
    expect(fake.Node.ArtifactPreview).toHaveBeenCalledTimes(5);
  });
});
