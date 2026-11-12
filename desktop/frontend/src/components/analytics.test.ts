// @vitest-environment happy-dom
// The Analytics view: Usage as it was, Traces (conversations → turns → a turn's timeline, which a
// running turn's stream extends), Commands; a chat's message opens its turn's trace.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fakeNode, flush, type Fake } from "../test/fake";

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

function thread(state = "done") {
  return {
    thread: "msg_a", title: "Fix the build", updated_at: iso(20_000),
    messages: [
      { id: "msg_a", thread: "msg_a", depth: 0, from: "user:me", to: "agent:claude", state, created_at: iso(0), updated_at: iso(20_000), finished_at: state === "done" ? iso(20_000) : undefined,
        events: [
          { seq: 1, at: T0 + 1000, kind: "tool_call", call: "c1", tool: "execute", title: "Terminal", status: "pending", command: "go test ./..." },
          { seq: 2, at: T0 + 4000, kind: "tool_call_update", call: "c1", status: "failed", exit: 1 },
          { seq: 3, at: T0 + 5000, kind: "tool_call", call: "c2", tool: "read", title: "Read a.go", status: "pending" },
          { seq: 4, at: T0 + 5500, kind: "tool_call_update", call: "c2", status: "completed" },
        ] },
      { id: "msg_b", thread: "msg_b", parent: "msg_a", depth: 1, from: "agent:claude", to: "agent:helper", state: "done", created_at: iso(6000), updated_at: iso(9000), finished_at: iso(9000), events: [] },
    ],
  };
}
const events = [
  ev(1, 1000, "tool_call", { id: "c1", kind: "execute", title: "Terminal", status: "pending" }),
  ev(2, 1100, "tool_call_update", { id: "c1", status: "pending", input: { command: "go test ./..." } }),
  ev(3, 4000, "tool_call_update", { id: "c1", status: "failed", output: "FAIL pkg 0.1s", exit_code: 1 }),
  ev(4, 5000, "tool_call", { id: "c2", kind: "read", title: "Read a.go", status: "pending" }),
  ev(5, 5500, "tool_call_update", { id: "c2", status: "completed" }),
];

async function setup() {
  fake = fakeNode();
  fake.Node.Usage.mockResolvedValue([]);
  fake.Node.Traces.mockImplementation(async (th: string) => [thread()].filter(x => !th || x.thread === th));
  fake.Node.Events.mockImplementation(async (_id: string, after: number) => events.filter(e => e.seq > after));
  const svelte = await import("svelte");
  const { analytics } = await import("../lib/analytics.svelte");
  analytics.tab = "usage"; analytics.thread = ""; analytics.turn = "";
  return { svelte, analytics };
}
const text = () => document.body.textContent ?? "";
const click = (el: Element | null | undefined) => { (el as HTMLElement).click(); };
async function settle(svelte: typeof import("svelte")) { for (let i = 0; i < 4; i++) { await flush(); svelte.flushSync(); } }

describe("Analytics", () => {
  beforeEach(() => { document.body.innerHTML = ""; });

  it("is titled Analytics, with Usage first and as it was", async () => {
    const { svelte } = await setup();
    const Analytics = (await import("./Analytics.svelte")).default;
    const c = svelte.mount(Analytics, { target: document.body });
    await settle(svelte);
    expect(document.querySelector("h1")!.textContent).toBe("Analytics");
    expect([...document.querySelectorAll("header .seg")[0].querySelectorAll("button")].map(b => b.textContent)).toEqual(["Usage", "Traces", "Commands"]);
    expect(fake.Node.Usage).toHaveBeenCalledWith(7, "agent");
    svelte.unmount(c);
  });

  it("lists conversations, opens one's turns and a turn's timeline, and shows what a call carried", async () => {
    const { svelte, analytics } = await setup();
    analytics.tab = "traces";
    const Analytics = (await import("./Analytics.svelte")).default;
    const c = svelte.mount(Analytics, { target: document.body });
    await settle(svelte);
    expect(fake.Node.Traces).toHaveBeenCalledWith("", 20);
    const row = document.querySelector("tbody tr.click")!;
    expect(row.textContent).toContain("Fix the build");
    expect(row.textContent).toContain("19.0s"); // active: from its first event to its end
    click(row);
    await settle(svelte);
    expect(fake.Node.Traces).toHaveBeenLastCalledWith("msg_a", 0);
    const turnRows = document.querySelectorAll("tbody tr.click");
    expect(turnRows.length).toBe(1); // its own turn; the helper's is under it
    click(turnRows[0]);
    await settle(svelte);
    const labels = [...document.querySelectorAll(".row .lab")].map(l => l.textContent);
    expect(labels.some(l => l?.includes("go test ./..."))).toBe(true);
    expect(labels.some(l => l?.includes("helper"))).toBe(true);
    // The filter keeps what has the text.
    const input = document.querySelector("input.flt") as HTMLInputElement;
    input.value = "read"; input.dispatchEvent(new Event("input"));
    await settle(svelte);
    expect(document.querySelectorAll("button.row").length).toBe(1);
    input.value = ""; input.dispatchEvent(new Event("input"));
    await settle(svelte);
    click([...document.querySelectorAll("button.row")].find(b => b.textContent?.includes("go test")));
    await settle(svelte);
    const det = document.querySelector("aside.det")!;
    expect(det.textContent).toContain("FAIL pkg 0.1s");
    expect(det.textContent).toContain("Error");
    expect(det.textContent).toContain("Exit code");
    svelte.unmount(c);
  });

  it("a running turn's timeline grows as the stream brings events", async () => {
    const { svelte, analytics } = await setup();
    fake.Node.Traces.mockImplementation(async () => [thread("running")]);
    analytics.tab = "traces"; analytics.thread = "msg_a"; analytics.turn = "msg_a";
    const Analytics = (await import("./Analytics.svelte")).default;
    const c = svelte.mount(Analytics, { target: document.body });
    await settle(svelte);
    const n = document.querySelectorAll("button.row").length;
    fake.emit("events:msg_a", { id: "msg_a", events: [ev(6, 7000, "tool_call", { id: "c3", kind: "search", title: "Search for main", status: "pending" })] });
    await settle(svelte);
    expect(document.querySelectorAll("button.row").length).toBe(n + 1);
    expect(text()).toContain("Search for main");
    svelte.unmount(c);
    expect(fake.listeners("events:msg_a")).toBe(0);
  });

  it("groups calls by what they ran", async () => {
    const { svelte, analytics } = await setup();
    analytics.tab = "commands";
    const Analytics = (await import("./Analytics.svelte")).default;
    const c = svelte.mount(Analytics, { target: document.body });
    await settle(svelte);
    const rows = [...document.querySelectorAll("tbody tr")].map(r => [...r.querySelectorAll("td")].slice(0, 6).map(td => td.textContent));
    expect(rows).toEqual([["go test", "1", "3.00s", "3.00s", "3.00s", "1"], ["read", "1", "500ms", "500ms", "500ms", "0"]]);
    svelte.unmount(c);
  });

  it("a chat's message opens its turn's trace", async () => {
    const { svelte, analytics } = await setup();
    const { app } = await import("../lib/state.svelte");
    const Message = (await import("./Message.svelte")).default;
    const m = { id: "msg_a", thread: "msg_a", from: "user:me", to: "agent:claude", depth: 0, state: "done", body: "x", created_at: iso(0), updated_at: iso(1) };
    const c = svelte.mount(Message, { target: document.body, props: { m } });
    svelte.flushSync();
    click(document.querySelector("button.trace"));
    expect(app.view).toBe("usage");
    expect([analytics.tab, analytics.thread, analytics.turn]).toEqual(["traces", "msg_a", "msg_a"]);
    svelte.unmount(c);
    app.view = "chat";
  });
});
