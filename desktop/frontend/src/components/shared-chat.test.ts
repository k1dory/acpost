// @vitest-environment happy-dom
// A chat shared by several agents: who the next message goes to is chosen in the composer.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fakeNode, flush, type Fake } from "../test/fake";

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
const msg = (id: string, to: string, extra: object = {}) =>
  ({ id, from: "user:me", to, thread: "T", depth: 0, state: "done", body: "q", reply: "a", created_at: "2026-11-12T10:00:00Z", updated_at: "2026-11-12T10:00:01Z", ...extra });

async function setup(msgs: any[], agents = ["claude", "codex"]) {
  svelte = await import("svelte");
  const { app } = await import("../lib/state.svelte");
  app.agents = agents.map(name => ({ name, address: "agent:" + name, driver: "acp", queued: 0, running: 0 }));
  app.conn = { ok: true };
  app.newTask();
  fake.Node.Thread.mockResolvedValue(msgs);
  await app.open({ key: "thread:T", kind: "thread", thread: "T", agent: msgs[0]?.to, title: "t", updated: "" } as any);
  const Composer = (await import("./Composer.svelte")).default;
  mounted.push(svelte.mount(Composer, { target: document.body }));
  svelte.flushSync();
  return app;
}
const textarea = () => document.querySelector("textarea") as HTMLTextAreaElement;
function type(text: string) {
  textarea().value = text;
  textarea().dispatchEvent(new Event("input", { bubbles: true }));
  svelte.flushSync();
}
const press = (key: string) => { textarea().dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true })); svelte.flushSync(); };
const chip = () => document.querySelector("button.chip.to") as HTMLButtonElement | null;

beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Events.mockResolvedValue([]);
  fake.Node.SendTask.mockImplementation(async (t: any) => msg("m9", t.to, { state: "queued", body: t.body }));
  document.body.innerHTML = "";
});
afterEach(() => { for (const m of mounted) svelte.unmount(m); mounted = []; });

describe("a chat shared by several agents", () => {
  it("the next message goes to whoever was asked last, not to the chat's first agent", async () => {
    const app = await setup([msg("m1", "agent:claude"), msg("m2", "agent:codex"), msg("m3", "agent:claude", { depth: 1, from: "agent:codex" })]);
    expect(app.target).toBe("agent:codex"); // m3 is what an agent asked another, not the person
    expect(chip()!.textContent).toContain("codex");
  });

  it("another agent of this Mac is called in from the chip, and the message goes to it in the same thread", async () => {
    const app = await setup([msg("m1", "agent:claude")]);
    expect(app.targets()).toEqual(["agent:claude", "agent:codex"]);
    chip()!.click();
    svelte.flushSync();
    const items = [...document.querySelectorAll(".menu.pick .mi")] as HTMLButtonElement[];
    expect(items.map(i => i.textContent!.trim())).toEqual(["claude", "codex"]);
    items[1].click();
    svelte.flushSync();
    expect(app.target).toBe("agent:codex");
    type("look at this");
    press("Enter");
    await flush(); await flush();
    expect(fake.Node.SendTask).toHaveBeenCalledTimes(1);
    expect(fake.Node.SendTask.mock.calls[0][0]).toMatchObject({ to: "agent:codex", thread: "T", body: "look at this" });
  });

  it("@name at the start of a message sends it to that agent, without the name", async () => {
    const app = await setup([msg("m1", "agent:claude")]);
    type("@Codex check the tests");
    press("Enter");
    await flush(); await flush();
    expect(fake.Node.SendTask.mock.calls[0][0]).toMatchObject({ to: "agent:codex", thread: "T", body: "check the tests" });
    expect(app.target).toBe("agent:codex");
  });

  it("@ alone offers the chat's agents; a name that is no agent's stays text", async () => {
    const app = await setup([msg("m1", "agent:claude")]);
    type("@co");
    expect([...document.querySelectorAll(".menu .mi.who")].map(i => i.textContent!.trim())).toEqual(["codex"]);
    press("Enter");
    expect(app.target).toBe("agent:codex");
    expect(textarea().value).toBe("");
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
    type("@nobody hello");
    press("Enter");
    await flush(); await flush();
    expect(fake.Node.SendTask.mock.calls[0][0]).toMatchObject({ to: "agent:codex", body: "@nobody hello" });
  });

  it("a chat with one agent to choose from has no chip; a cloud sandbox's chat takes no agent of this Mac", async () => {
    let app = await setup([msg("m1", "agent:claude")], ["claude"]);
    expect(chip()).toBeNull();
    expect(textarea().placeholder).not.toContain("@");
    for (const m of mounted) svelte.unmount(m);
    mounted = [];
    vi.resetModules();
    app = await setup([msg("m1", "agent:sb_1")]);
    expect(app.targets()).toEqual(["agent:sb_1"]);
    expect(chip()).toBeNull();
  });
});
