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
    const kid = document.querySelector(".folder .kids .row.kid") as HTMLButtonElement;
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
    expect(document.querySelector(".kids")).toBeNull();
    expect([...document.querySelectorAll(".list .row .title")].map(e => e.textContent)).toEqual(["Team invites API"]);
  });
});
