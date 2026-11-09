// @vitest-environment happy-dom
// Round 2 of the review (codex-second-desk): components mounted for real, the bindings mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
let mount: typeof import("svelte").mount, unmount: typeof import("svelte").unmount;
let flushSync: typeof import("svelte").flushSync;
import { fakeNode, deferred, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

async function setup() {
  ({ mount, unmount, flushSync } = await import("svelte"));
  const { app } = await import("../lib/state.svelte");
  app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
  app.conn = { ok: true };
  app.home = "/Users/me";
  return app;
}
function type(el: HTMLInputElement | HTMLTextAreaElement, text: string) {
  el.value = text;
  el.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}
const button = (label: string) => [...document.querySelectorAll("button")].find(b => b.textContent?.trim() === label) as HTMLButtonElement;
async function settle() { for (let i = 0; i < 3; i++) { await flush(); flushSync(); } }

let mounted: any[] = [];
beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Agents.mockResolvedValue([]);
  fake.Node.Events.mockResolvedValue([]);
  document.body.innerHTML = "";
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; vi.useRealTimers(); });

describe("a form's new record has its id from the draft (finding 5)", () => {
  it("a backlog item saved again after a lost answer is the same item", async () => {
    await setup();
    fake.Node.Backlog.mockResolvedValue([]);
    fake.Node.PutBacklogItem.mockRejectedValueOnce(new Error("lost")).mockResolvedValue({});
    const Board = (await import("./Board.svelte")).default;
    mounted.push(mount(Board, { target: document.body }));
    await settle();
    button("Write down a task for later").click(); flushSync();
    type(document.querySelector("input.input") as HTMLInputElement, "fix the tests");
    button("Save").click(); await settle();
    button("Save").click(); await settle();
    const ids = fake.Node.PutBacklogItem.mock.calls.map(c => c[0].id);
    expect(ids.length).toBe(2);
    expect(ids[0]).toMatch(/^bl_[0-9a-f]{32}$/);
    expect(ids[1]).toBe(ids[0]);
  });

  it("a schedule saved again after a lost answer is the same schedule; the next new one another", async () => {
    await setup();
    fake.Node.Schedules.mockResolvedValue([]);
    fake.Node.ForgeAccounts.mockResolvedValue([]);
    fake.Node.PutSchedule.mockRejectedValueOnce(new Error("lost")).mockResolvedValue({});
    const Settings = (await import("./Settings.svelte")).default;
    mounted.push(mount(Settings, { target: document.body }));
    await settle();
    button("Schedules").click(); await settle();
    const fill = () => {
      button("New schedule").click(); flushSync();
      type(document.querySelector("input.input") as HTMLInputElement, "morning");
      type(document.querySelector("textarea.input") as HTMLTextAreaElement, "review");
    };
    fill();
    expect(button("Delete")).toBeUndefined(); // nothing saved to delete yet
    button("Save").click(); await settle();
    button("Save").click(); await settle();
    fill();
    button("Save").click(); await settle();
    const calls = fake.Node.PutSchedule.mock.calls.map(c => c[0]);
    expect(calls.length).toBe(3);
    expect(calls[0].id).toMatch(/^sch_[0-9a-f]{32}$/);
    expect(calls[1].id).toBe(calls[0].id);
    expect(calls[2].id).not.toBe(calls[0].id);
    expect("isNew" in calls[0]).toBe(false);
  });
});
