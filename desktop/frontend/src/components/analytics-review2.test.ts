// @vitest-environment happy-dom
// Review round 2 of the Analytics view: a conversation's refresh slower than its interval is waited
// for, not overtaken; a turn's last events that fail to read are retried, shown, and read again by
// Refresh; Commands warn when a turn a conversation asked was cut.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fakeNode, flush, deferred, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("../lib/api", async () => {
  const errors = await import("../lib/errors");
  const b = await import("../lib/base64");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, ...b, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f) };
});
vi.mock("@wailsio/runtime", () => ({ Clipboard: { SetText: vi.fn() }, Browser: { OpenURL: vi.fn() }, Events: { On: () => () => {} } }));

const T0 = Date.parse("2026-11-12T10:00:00Z");
const iso = (dt: number) => new Date(T0 + dt).toISOString();
const ev = (seq: number, dt: number, kind: string, data: unknown) => ({ seq, time: iso(dt), kind, data: JSON.stringify(data) });
function turn(id: string, state: string, over: any = {}) {
  return { id, thread: "msg_a", depth: 0, from: "user:me", to: "agent:claude", state, created_at: iso(0), updated_at: iso(1000),
    finished_at: state === "done" ? iso(20_000) : undefined, events: [], ...over };
}
const th = (messages: any[]) => ({ thread: "msg_a", title: "Chat A", updated_at: iso(1000), messages });

async function setup() {
  fake = fakeNode();
  fake.Node.Usage.mockResolvedValue([]);
  const svelte = await import("svelte");
  const { analytics } = await import("../lib/analytics.svelte");
  analytics.tab = "traces"; analytics.thread = ""; analytics.turn = "";
  const Analytics = (await import("./Analytics.svelte")).default;
  return { svelte, analytics, mount: () => svelte.mount(Analytics, { target: document.body }) };
}
async function settle(svelte: typeof import("svelte")) { for (let i = 0; i < 6; i++) { await flush(); svelte.flushSync(); } }

describe("Analytics, review round 2", () => {
  beforeEach(() => { document.body.innerHTML = ""; });
  afterEach(() => { vi.useRealTimers(); });

  it("a refresh slower than the interval is waited for, and its answer applied", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const { svelte, analytics, mount } = await setup();
    const slow = deferred<any[]>();
    fake.Node.Traces.mockResolvedValueOnce([th([turn("msg_a", "running")])]).mockImplementation(() => slow.promise);
    fake.Node.Events.mockResolvedValue([]);
    analytics.thread = "msg_a";
    const c = mount();
    await settle(svelte);
    expect(document.querySelector("tbody tr.click")!.textContent).toContain("running");
    vi.advanceTimersByTime(4100);
    await settle(svelte);
    const asked = fake.Node.Traces.mock.calls.length;
    vi.advanceTimersByTime(3 * 4000); // the answer takes longer than three intervals
    await settle(svelte);
    expect(fake.Node.Traces.mock.calls.length).toBe(asked); // no read started over the one out
    slow.resolve([th([turn("msg_a", "done")])]);
    await settle(svelte);
    expect(document.querySelector("tbody tr.click")!.textContent).not.toContain("running");
    svelte.unmount(c);
  });

  it("a turn's last events that fail to read are retried, the failure shown, and Refresh reads them", async () => {
    const { svelte, analytics, mount } = await setup();
    let state = "running", broken = false;
    fake.Node.Traces.mockImplementation(async () => [th([turn("msg_a", state)])]);
    let store = [ev(1, 1000, "tool_call", { id: "c1", kind: "execute", status: "pending" })];
    fake.Node.Events.mockImplementation(async (_: string, after: number) => {
      if (broken) throw new Error("the node did not answer");
      return store.filter(e => e.seq > after);
    });
    analytics.thread = "msg_a"; analytics.turn = "msg_a";
    const c = mount();
    await settle(svelte);
    store = [...store, ev(2, 3000, "tool_call_update", { id: "c1", status: "failed" })];
    broken = true;
    state = "done";
    const before = fake.Node.Events.mock.calls.length;
    fake.emit("events:msg_a", { id: "msg_a", final: { id: "msg_a", state: "done" } });
    await settle(svelte);
    await new Promise(r => setTimeout(r, 1700)); // the retries' pauses
    await settle(svelte);
    expect(fake.Node.Events.mock.calls.length - before).toBe(3);
    expect(document.querySelector(".rd")?.textContent).toContain("the node did not answer");
    expect(document.querySelector(".stats")!.textContent).toMatch(/Errors\s*0/);
    broken = false;
    (document.querySelector('header button[title="Refresh"]') as HTMLElement).click();
    await settle(svelte);
    expect(document.querySelector(".stats")!.textContent).toMatch(/Errors\s*1/);
    expect(document.querySelector(".rd")).toBeNull();
    svelte.unmount(c);
  });

  it("Commands warn when a turn a conversation asked was cut by the node", async () => {
    const { svelte, analytics, mount } = await setup();
    const helper = turn("msg_h", "done", { thread: "msg_h", parent: "msg_a", depth: 1, to: "agent:helper",
      events: [{ seq: 1, at: T0 + 1000, kind: "tool_call", call: "h", tool: "read" }, { seq: 2, at: T0 + 2000, kind: "truncated" }] });
    fake.Node.Traces.mockResolvedValue([th([turn("msg_a", "done"), helper])]);
    analytics.tab = "commands";
    const c = mount();
    await settle(svelte);
    expect(document.querySelector("tbody tr")!.textContent).toContain("read");
    expect(document.querySelector(".note")?.textContent).toContain("not shown");
    svelte.unmount(c);
  });
});
