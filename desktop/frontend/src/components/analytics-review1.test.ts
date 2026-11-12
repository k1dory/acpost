// @vitest-environment happy-dom
// Review round 1 of the Analytics view: a running turn's history is not lost to its stream; another
// running turn shown is polled; a turn found over is read to its end; a conversation is refreshed
// while a turn it asked runs; a chat chosen in Commands is read by itself; a turn the node stopped
// keeping says so.
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
const call = (seq: number, id: string, title: string, status = "pending") => ev(seq, seq * 1000, seq === 0 ? "tool_call" : "tool_call", { id, kind: "read", title, status });

function turn(id: string, state: string, over: any = {}) {
  return { id, thread: "msg_a", depth: 0, from: "user:me", to: "agent:claude", state, created_at: iso(0), updated_at: iso(1000),
    finished_at: state === "done" ? iso(20_000) : undefined, events: [], ...over };
}
const th = (messages: any[], over: any = {}) => ({ thread: "msg_a", title: "Chat A", updated_at: iso(1000), messages, ...over });

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
const labels = () => [...document.querySelectorAll("button.row .lab")].map(l => l.textContent ?? "");

describe("Analytics, review round 1", () => {
  beforeEach(() => { document.body.innerHTML = ""; });
  afterEach(() => { vi.useRealTimers(); });

  it("a live batch while the first read is out does not make the history be skipped", async () => {
    const { svelte, analytics, mount } = await setup();
    fake.Node.Traces.mockResolvedValue([th([turn("msg_a", "running")])]);
    const history = [call(1, "c1", "Read one"), call(2, "c2", "Read two"), call(3, "c3", "Read three")];
    const first = deferred<any[]>();
    fake.Node.Events.mockImplementationOnce(() => first.promise).mockImplementation(async (_: string, after: number) => history.filter(e => e.seq > after));
    analytics.thread = "msg_a"; analytics.turn = "msg_a";
    const c = mount();
    await settle(svelte);
    fake.emit("events:msg_a", { id: "msg_a", events: [call(4, "c4", "Read four")] }); // ahead of the read
    first.resolve(history.slice(0, 1));
    await settle(svelte);
    expect(labels().map(l => l.replace(/^read/, ""))).toEqual(["Read one", "Read two", "Read three", "Read four"]);
    svelte.unmount(c);
  });

  it("another running turn shown is polled, and its answers are applied", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const { svelte, analytics, mount } = await setup();
    fake.Node.Traces.mockResolvedValue([th([turn("msg_a", "running"), turn("msg_b", "running")])]);
    let bEvents: any[] = [];
    fake.Node.Events.mockImplementation(async (id: string, after: number) => (id === "msg_b" ? bEvents : [call(1, "a1", "Read A")]).filter(e => e.seq > after));
    analytics.thread = "msg_a"; analytics.turn = "msg_a";
    const c = mount();
    await settle(svelte);
    analytics.turn = "msg_b";
    await settle(svelte);
    expect(labels()).toEqual([]);
    bEvents = [call(1, "b1", "Read B")];
    vi.advanceTimersByTime(2100);
    await settle(svelte);
    expect(fake.Node.Events.mock.calls.slice(-2)).toEqual([["msg_b", 0], ["msg_b", 1]]); // the poll read B's events, to their end
    expect(labels().join()).toContain("Read B");
    svelte.unmount(c);
  });

  it("a turn found over is read to its end: a call that failed at the last counts", async () => {
    const { svelte, analytics, mount } = await setup();
    let state = "running";
    fake.Node.Traces.mockImplementation(async () => [th([turn("msg_a", state)])]);
    let store = [ev(1, 1000, "tool_call", { id: "c1", kind: "execute", status: "pending" })];
    fake.Node.Events.mockImplementation(async (_: string, after: number) => store.filter(e => e.seq > after));
    analytics.thread = "msg_a"; analytics.turn = "msg_a";
    const c = mount();
    await settle(svelte);
    // The turn ends; its last event is in the store, not yet read, when its state is known.
    store = [...store, ev(2, 3000, "tool_call_update", { id: "c1", status: "failed", output: "boom" })];
    state = "done";
    fake.emit("events:msg_a", { id: "msg_a", final: { id: "msg_a", state: "done" } });
    await settle(svelte);
    const stats = document.querySelector(".stats")!.textContent!;
    expect(stats).toMatch(/Errors\s*1/);
    svelte.unmount(c);
  });

  it("a conversation is read again while a turn it asked still runs", async () => {
    vi.useFakeTimers({ toFake: ["setInterval", "clearInterval"] });
    const { svelte, analytics, mount } = await setup();
    const child = turn("msg_c", "running", { thread: "msg_c", parent: "msg_a", depth: 1, to: "agent:helper" });
    fake.Node.Traces.mockResolvedValue([th([turn("msg_a", "done"), child])]);
    fake.Node.Events.mockResolvedValue([]);
    analytics.thread = "msg_a";
    const c = mount();
    await settle(svelte);
    const before = fake.Node.Traces.mock.calls.length;
    vi.advanceTimersByTime(4100);
    await settle(svelte);
    expect(fake.Node.Traces.mock.calls.length).toBeGreaterThan(before);
    // And in the list: the conversation is running.
    analytics.thread = "";
    await settle(svelte);
    expect(document.querySelector("tbody tr.click")!.textContent).toContain("running");
    svelte.unmount(c);
  });

  it("a chat chosen in Commands is read by itself, not taken from the list where it may be cut", async () => {
    const { svelte, analytics, mount } = await setup();
    const full = turn("msg_a", "done", { events: [{ seq: 1, at: T0 + 1000, kind: "tool_call", call: "c", tool: "read", status: "pending" }, { seq: 2, at: T0 + 2000, kind: "tool_call_update", call: "c", status: "completed" }] });
    fake.Node.Traces.mockImplementation(async (t: string) => t ? [th([full])] : [th([turn("msg_a", "done", { cut: true })], { cut: true })]);
    analytics.tab = "commands"; analytics.thread = "msg_a";
    const c = mount();
    await settle(svelte);
    expect(fake.Node.Traces).toHaveBeenCalledWith("msg_a", 0);
    const row = document.querySelector("tbody tr")!;
    expect(row.textContent).toContain("read");
    expect(row.textContent).not.toContain("No calls yet");
    svelte.unmount(c);
  });

  it("a turn whose events the node stopped keeping says its numbers are short", async () => {
    const { svelte, analytics, mount } = await setup();
    fake.Node.Traces.mockResolvedValue([th([turn("msg_a", "done")])]);
    fake.Node.Events.mockImplementation(async (_: string, after: number) => [call(1, "c1", "Read one"), ev(2, 2000, "truncated", "later events were not kept")].filter(e => e.seq > after));
    analytics.thread = "msg_a"; analytics.turn = "msg_a";
    const c = mount();
    await settle(svelte);
    expect(document.querySelector(".note.warn")?.textContent).toContain("kept no more");
    svelte.unmount(c);
  });
});
