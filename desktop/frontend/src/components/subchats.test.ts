// @vitest-environment happy-dom
// The sidebar lists a chat an agent started under the chat it came from.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fakeNode, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

let svelte: typeof import("svelte");
let mounted: any[] = [];
const chat = (thread: string, title: string, extra: object = {}) =>
  ({ key: "thread:" + thread, kind: "thread", thread, agent: "agent:claude", title, updated: "2026-11-12T10:00:00Z", ...extra });

beforeEach(() => { vi.resetModules(); fake = fakeNode(); document.body.innerHTML = ""; });
afterEach(() => { for (const m of mounted) svelte.unmount(m); mounted = []; });

describe("the sidebar's chats", () => {
  it("a chat an agent started is under its chat, with its agent and state; filtered, the list is flat", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.conn = { ok: true };
    app.folders = [{ id: "f1", name: "Work", chats: ["thread:A"] }] as any;
    app.chats = [
      chat("A", "Team invites", { folder: "f1" }),
      chat("B", "Team invites API", { parent: "thread:A", agent: "agent:codex", running: 1 }),
      chat("C", "Sentry errors"),
      chat("D", "Orphan", { parent: "thread:gone" }),
    ] as any;
    const Sidebar = (await import("./Sidebar.svelte")).default;
    mounted.push(svelte.mount(Sidebar, { target: document.body }));
    svelte.flushSync();
    const kid = document.querySelector(".folder .row.kid") as HTMLButtonElement;
    expect(kid.textContent).toContain("Team invites API");
    expect(kid.querySelector(".dot.running")).toBeTruthy();
    // Listed once: not also among the loose chats; one whose chat is not listed is a chat like any.
    const titles = [...document.querySelectorAll(".list .row .title")].map(e => e.textContent);
    expect(titles).toEqual(["Team invites", "Team invites API", "Sentry errors", "Orphan"]);
    fake.Node.Thread.mockResolvedValue([]);
    kid.click();
    expect(app.chat?.key).toBe("thread:B");
    const input = document.querySelector(".search input") as HTMLInputElement;
    input.value = "api"; input.dispatchEvent(new Event("input", { bubbles: true }));
    svelte.flushSync();
    expect(document.querySelector(".row.kid")).toBeNull();
    expect([...document.querySelectorAll(".list .row .title")].map(e => e.textContent)).toEqual(["Team invites API"]);
  });

  it("a chain deeper than the indentation goes: every chat is listed once", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.conn = { ok: true };
    app.folders = [];
    app.chats = Array.from({ length: 300 }, (_, i) => chat("T" + i, "chat " + i, i ? { parent: "thread:T" + (i - 1) } : {})) as any;
    const Sidebar = (await import("./Sidebar.svelte")).default;
    mounted.push(svelte.mount(Sidebar, { target: document.body }));
    svelte.flushSync();
    expect([...document.querySelectorAll(".list .row .title")].map(e => e.textContent)).toEqual(app.chats.map(c => c.title));
  });

  it("a subagent active now is seen at once, though its chat is older than the chats shown; it has no folder of its own to move to", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.conn = { ok: true };
    app.folders = [{ id: "f1", name: "Work", chats: [] }] as any;
    app.chats = [chat("K", "active kid", { parent: "thread:OLD", running: 1 }),
      ...Array.from({ length: 130 }, (_, i) => chat("R" + i, "root " + i)), chat("OLD", "old parent")] as any;
    const Sidebar = (await import("./Sidebar.svelte")).default;
    mounted.push(svelte.mount(Sidebar, { target: document.body }));
    svelte.flushSync();
    const titles = [...document.querySelectorAll(".list .row .title")].map(e => e.textContent);
    expect(titles.slice(0, 3)).toEqual(["old parent", "active kid", "root 0"]);
    const kid = document.querySelector(".row.kid") as HTMLButtonElement;
    kid.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    svelte.flushSync();
    expect(document.querySelector(".ctx")!.textContent).toContain("Rename");
    expect(document.querySelector(".ctx")!.textContent).not.toContain("Move to");
  });

  it("found by the filter, a chat that came from another is still not moved to a folder on its own", async () => {
    svelte = await import("svelte");
    const { app } = await import("../lib/state.svelte");
    app.conn = { ok: true };
    app.folders = [{ id: "f1", name: "Work", chats: ["thread:A"] }, { id: "f2", name: "Elsewhere", chats: [] }] as any;
    app.chats = [chat("A", "parent", { folder: "f1" }), chat("B", "the kid", { parent: "thread:A" }), chat("C", "kindred")] as any;
    const Sidebar = (await import("./Sidebar.svelte")).default;
    mounted.push(svelte.mount(Sidebar, { target: document.body }));
    svelte.flushSync();
    const input = document.querySelector(".search input") as HTMLInputElement;
    input.value = "ki"; input.dispatchEvent(new Event("input", { bubbles: true }));
    svelte.flushSync();
    const rows = [...document.querySelectorAll(".list button.row")] as HTMLButtonElement[];
    const kid = rows.find(r => r.textContent!.includes("the kid"))!, own = rows.find(r => r.textContent!.includes("kindred"))!;
    kid.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    svelte.flushSync();
    expect(document.querySelector(".ctx")!.textContent).not.toContain("Move to");
    document.body.click();
    svelte.flushSync();
    own.dispatchEvent(new MouseEvent("contextmenu", { bubbles: true, cancelable: true }));
    svelte.flushSync();
    expect(document.querySelector(".ctx")!.textContent).toContain("Move to");
  });
});
