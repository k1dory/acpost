// @vitest-environment happy-dom
// Components mounted for real (happy-dom), the bindings and the Wails runtime mocked.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
// Svelte is imported after vi.resetModules(), with the components: one runtime for both.
let mount: typeof import("svelte").mount, unmount: typeof import("svelte").unmount;
let flushSync: typeof import("svelte").flushSync, tick: typeof import("svelte").tick;
import { fakeNode, deferred, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

const msg = (id: string, thread: string, state = "queued") =>
  ({ id, from: "user:me", to: "agent:claude", thread, depth: 0, state, created_at: "", updated_at: "" });

async function setup() {
  ({ mount, unmount, flushSync, tick } = await import("svelte"));
  const { app } = await import("../lib/state.svelte");
  app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
  app.conn = { ok: true };
  app.draft.agent = "agent:claude";
  return app;
}
function type(ta: HTMLTextAreaElement, text: string) {
  ta.value = text;
  ta.dispatchEvent(new Event("input", { bubbles: true }));
  flushSync();
}
const textarea = () => document.querySelector("textarea") as HTMLTextAreaElement;
const sendButton = () => document.querySelector("button.send") as HTMLButtonElement;

let mounted: any[] = [];
beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Agents.mockResolvedValue([]);
  fake.Node.Events.mockResolvedValue([]);
  document.body.innerHTML = "";
});
afterEach(() => { for (const m of mounted) unmount(m); mounted = []; vi.useRealTimers(); vi.unstubAllGlobals(); });

describe("the composer's draft", () => {
  it("survives the composer being taken off screen and back, as in an outage (R1.1)", async () => {
    const app = await setup();
    app.thread = "T"; app.target = "agent:claude";
    const Composer = (await import("./Composer.svelte")).default;
    let c = mount(Composer, { target: document.body });
    flushSync();
    type(textarea(), "keep me");
    fake.Node.SendTask.mockRejectedValue(new Error("down"));
    sendButton().click();
    await flush(); flushSync();
    unmount(c);                       // the node is down: the conversation is not shown
    c = mount(Composer, { target: document.body });
    mounted.push(c);
    flushSync();
    expect(textarea().value).toBe("keep me");
  });

  it("keeps what was typed while a new task's first message went, in the chat it becomes (R1.5)", async () => {
    const app = await setup();
    const Conversation = (await import("./Conversation.svelte")).default;
    mounted.push(mount(Conversation, { target: document.body }));
    flushSync();
    type(textarea(), "first");
    const sent = deferred();
    fake.Node.SendTask.mockReturnValue(sent.promise);
    sendButton().click();
    flushSync();
    type(textarea(), "first\nand then");
    sent.resolve(msg("m1", "T1"));
    await flush(); await tick(); flushSync();
    expect(app.thread).toBe("T1");
    expect(document.querySelectorAll("textarea").length).toBe(1);
    expect(textarea().value).toBe("and then");
  });
});

// An IntersectionObserver the test drives.
class FakeIO {
  static all: FakeIO[] = [];
  constructor(public cb: (es: { isIntersecting: boolean }[]) => void) { FakeIO.all.push(this); }
  observe() {}
  disconnect() {}
  show(v: boolean) { this.cb([{ isIntersecting: v }]); flushSync(); }
}

describe("subtasks (R1.7)", () => {
  it("asks the tree only once on screen, and polls only while there", async () => {
    vi.useFakeTimers();
    FakeIO.all = [];
    vi.stubGlobal("IntersectionObserver", FakeIO);
    await setup();
    fake.Node.Tree.mockResolvedValue([]);
    const Subtasks = (await import("./Subtasks.svelte")).default;
    mounted.push(mount(Subtasks, { target: document.body, props: { msg: "m1", running: true } }));
    flushSync();
    await vi.advanceTimersByTimeAsync(10_000);
    expect(fake.Node.Tree).not.toHaveBeenCalled();     // off screen: not asked
    FakeIO.all[0].show(true);
    await vi.advanceTimersByTimeAsync(0);
    expect(fake.Node.Tree).toHaveBeenCalledTimes(1);
    await vi.advanceTimersByTimeAsync(4_000);
    expect(fake.Node.Tree).toHaveBeenCalledTimes(2);    // running, on screen: polled
    FakeIO.all[0].show(false);
    await vi.advanceTimersByTimeAsync(20_000);
    expect(fake.Node.Tree).toHaveBeenCalledTimes(2);    // left the screen: no more
  });
});
