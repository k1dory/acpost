// @vitest-environment happy-dom
// The library view mounted for real (happy-dom), the bindings and the Wails runtime mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
let mount: typeof import("svelte").mount, unmount: typeof import("svelte").unmount, flushSync: typeof import("svelte").flushSync;
import { fakeNode, flush, type Fake } from "../test/fake";

let fake: Fake;
const openURL = vi.fn();
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: (u: string) => openURL(u) }, Events: { On: () => () => {} } }));

// An IntersectionObserver the test drives, element by element.
class FakeIO {
  static all: FakeIO[] = [];
  el: Element | null = null;
  constructor(public cb: (es: { isIntersecting: boolean }[]) => void) { FakeIO.all.push(this); }
  observe(el: Element) { this.el = el; }
  disconnect() {}
  show(v: boolean) { this.cb([{ isIntersecting: v }]); flushSync(); }
}
const ioOf = (el: Element) => FakeIO.all.find(o => o.el === el)!;

const now = new Date().toISOString();
const items = [
  { kind: "file", name: "chart.png", type: "image/png", size: 10, msg: "m1", thread: "T1", title: "Make a chart", agent: "agent:claude", at: now },
  { kind: "change", name: "report.md", type: "text/markdown", size: 20, path: "/w/report.md", status: "A", msg: "m2", thread: "T2", title: "Write it", agent: "agent:codex", at: now },
  { kind: "link", name: "site", type: "text/html", size: -1, uri: "https://example.com/", msg: "m3", thread: "T1", agent: "agent:claude", at: now },
  { kind: "change", name: "old.pdf", size: -1, path: "/w/old.pdf", msg: "m4", agent: "agent:x", at: now, gone: true },
];

let mounted: any[] = [];
beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  FakeIO.all = [];
  openURL.mockReset();
  vi.stubGlobal("IntersectionObserver", FakeIO);
  fake.Node.Chats.mockResolvedValue({ items: [] });
  document.body.innerHTML = "";
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; vi.useRealTimers(); vi.unstubAllGlobals(); });

async function show() {
  ({ mount, unmount, flushSync } = await import("svelte"));
  const { app } = await import("../lib/state.svelte");
  app.conn = { ok: true } as any;
  const Library = (await import("./Library.svelte")).default;
  mounted.push(mount(Library, { target: document.body }));
  flushSync();
  await flush(); flushSync();
  return app;
}
const cards = () => [...document.querySelectorAll(".lc")];

describe("the artifacts library", () => {
  it("lists what the agents left in their working folders with the chats', and shows one sort alone", async () => {
    fake.Node.Artifacts.mockResolvedValue({ items: [items[1]], next: "" });
    fake.Node.FoundArtifacts.mockResolvedValue([
      { kind: "change", name: "landing/index.html", type: "text/html", size: 5, path: "/private/tmp/claude-501/p/s/scratchpad/landing/index.html", msg: "", title: "site", agent: "agent:claude", at: now },
      { kind: "change", name: "report.pdf", type: "application/pdf", size: 5, path: "/private/tmp/claude-501/p/s/scratchpad/report.pdf", msg: "", title: "site", agent: "agent:claude", at: now },
      { kind: "change", name: "shot.png", type: "image/png", size: 5, path: "/private/tmp/claude-501/p/s/images/shot.png", msg: "", title: "site", agent: "agent:claude", at: now },
    ]);
    await show();
    expect(fake.Node.FoundArtifacts).toHaveBeenCalledWith("");
    expect(cards().length).toBe(4);
    const sorts = [...document.querySelectorAll<HTMLButtonElement>(".sort")];
    sorts[1].click(); flushSync();
    expect(cards().map(c => c.querySelector(".nm")!.textContent)).toEqual(["landing/index.html"]);
    sorts[2].click(); flushSync();
    expect(cards().length).toBe(2); // report.md of a chat, report.pdf found
    sorts[3].click(); flushSync();
    expect(cards().map(c => c.querySelector(".nm")!.textContent)).toEqual(["shot.png"]);
  });
  it("lists the found files when the node cannot be asked", async () => {
    fake.Node.Artifacts.mockRejectedValue(new Error("no node"));
    fake.Node.FoundArtifacts.mockResolvedValue([{ kind: "change", name: "report.pdf", size: 5, path: "/t/report.pdf", msg: "", at: now }]);
    await show();
    expect(cards().length).toBe(1);
  });
  it("lists every chat's artifacts, asking for a card's content only once it is near the screen", async () => {
    fake.Node.Artifacts.mockResolvedValue({ items, next: "" });
    fake.Node.ArtifactPreview.mockImplementation(async (msg: string, name: string) =>
      name === "chart.png" ? { name, type: "image/png", size: 10, data: "iVBORw0K" } : { name, type: "text/markdown", size: 20, data: btoa("# Report\nline two\n") });
    await show();
    expect(fake.Node.Artifacts).toHaveBeenCalledWith("", "", 60);
    expect(cards().length).toBe(4);
    expect(document.body.textContent).toContain("Make a chart");
    expect(document.body.textContent).toContain("no longer there");
    expect(fake.Node.ArtifactPreview).not.toHaveBeenCalled(); // nothing on screen yet

    for (const c of cards()) ioOf(c).show(true);
    await flush(); flushSync();
    // A reply's file by its message, a file of this Mac by its path; a web link and a file gone, never.
    expect(fake.Node.ArtifactPreview.mock.calls).toEqual([["m1", "chart.png", ""], ["", "report.md", "/w/report.md"]]);
    expect(cards()[0].querySelector("img")?.getAttribute("src")).toBe("data:image/png;base64,iVBORw0K");
    expect(cards()[1].querySelector("pre")?.textContent).toBe("# Report\nline two\n");
    ioOf(cards()[0]).show(true);
    expect(fake.Node.ArtifactPreview).toHaveBeenCalledTimes(2); // asked once
  });

  it("filters by name or address once typing pauses, and refreshes", async () => {
    fake.Node.Artifacts.mockResolvedValue({ items, next: "" });
    await show();
    vi.useFakeTimers();
    const input = document.querySelector(".filter input") as HTMLInputElement;
    for (const v of ["r", "re", "rep"]) { input.value = v; input.dispatchEvent(new Event("input", { bubbles: true })); flushSync(); }
    await vi.advanceTimersByTimeAsync(100);
    expect(fake.Node.Artifacts).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(300);
    expect(fake.Node.Artifacts).toHaveBeenCalledTimes(2);
    expect(fake.Node.Artifacts).toHaveBeenLastCalledWith("rep", "", 60);
    (document.querySelector('header button[aria-label="Refresh"]') as HTMLButtonElement).click();
    await vi.advanceTimersByTimeAsync(0);
    expect(fake.Node.Artifacts).toHaveBeenCalledTimes(3);
  });

  it("asks for the next page at the end of the list, without repeats", async () => {
    fake.Node.Artifacts.mockResolvedValueOnce({ items: items.slice(0, 2), next: "c1" }).mockResolvedValueOnce({ items: items.slice(1), next: "" });
    await show();
    const tail = document.querySelector(".tail")!;
    ioOf(tail).show(true);
    await flush(); flushSync();
    expect(fake.Node.Artifacts).toHaveBeenLastCalledWith("", "c1", 60);
    expect(cards().length).toBe(4);
    expect(document.querySelector(".tail")).toBeNull();
  });

  it("opens an artifact in the viewer, a web link in the browser, and a chat", async () => {
    fake.Node.Artifacts.mockResolvedValue({ items, next: "" });
    fake.Node.LocalFile.mockResolvedValue({ name: "report.md", type: "text/markdown", size: 8, path: "/w/report.md", data: btoa("# Report") });
    const app = await show();
    (cards()[2].querySelector(".hit") as HTMLButtonElement).click();
    expect(openURL).toHaveBeenCalledWith("https://example.com/");

    (cards()[1].querySelector(".hit") as HTMLButtonElement).click();
    flushSync(); await flush(); flushSync();
    expect(fake.Node.LocalFile).toHaveBeenCalledWith("/w/report.md", "");
    expect(document.querySelector(".av .md-wrap")?.textContent).toContain("Report");

    fake.Node.Chats.mockResolvedValue({ items: [{ key: "thread:T2", kind: "thread", thread: "T2", title: "Write it", updated: now }] });
    fake.Node.Thread.mockResolvedValue([]);
    const chat = [...document.querySelectorAll("header button")].find(b => b.textContent?.includes("Open the chat")) as HTMLButtonElement;
    chat.click();
    await flush(); flushSync();
    expect(app.view).toBe("chat");
    expect(app.chat?.key).toBe("thread:T2");
  });

  it("says when there is nothing yet, and what failed", async () => {
    fake.Node.Artifacts.mockRejectedValue(new Error("node down"));
    await show();
    expect(document.querySelector(".err")?.textContent).toContain("node down");
    expect(document.body.textContent).toContain("No artifacts yet");
  });
});
