// @vitest-environment happy-dom
// Review round 3 of the Analytics view: a turn two conversations hold has its calls counted once in
// Commands; a first read that fails is shown with Retry, and a Refresh that works clears it.
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
function turn(id: string, over: any = {}) {
  return { id, thread: "msg_a", depth: 0, from: "user:me", to: "agent:claude", state: "done", created_at: iso(0), updated_at: iso(20_000), finished_at: iso(20_000), events: [], ...over };
}

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

describe("Analytics, review round 3", () => {
  beforeEach(() => { document.body.innerHTML = ""; });

  it("a subagent's turn its chat was continued from is counted once in Commands", async () => {
    const { svelte, analytics, mount } = await setup();
    const call = [{ seq: 1, at: T0 + 1000, kind: "tool_call", call: "c", tool: "read" }, { seq: 2, at: T0 + 2000, kind: "tool_call_update", call: "c", status: "completed" }];
    const sub = turn("msg_s", { thread: "msg_s", parent: "msg_a", depth: 1, to: "agent:helper", events: call });
    fake.Node.Traces.mockResolvedValue([
      { thread: "msg_a", title: "Parent", updated_at: iso(1), messages: [turn("msg_a"), sub] },
      { thread: "msg_s", title: "Continued", updated_at: iso(2), messages: [sub, turn("msg_s2", { thread: "msg_s" })] },
    ]);
    analytics.tab = "commands";
    const c = mount();
    await settle(svelte);
    const cells = [...document.querySelectorAll("tbody tr")[0].querySelectorAll("td")].slice(0, 3).map(td => td.textContent);
    expect(cells).toEqual(["read", "1", "1.00s"]);
    svelte.unmount(c);
  });

  it("a first read that fails is shown with Retry; a Refresh that works clears it and the numbers are right", async () => {
    const { svelte, analytics, mount } = await setup();
    fake.Node.Traces.mockResolvedValue([{ thread: "msg_a", title: "A", updated_at: iso(1), messages: [turn("msg_a")] }]);
    let broken = true;
    const store = [ev(1, 1000, "tool_call", { id: "c1", kind: "execute", status: "pending" }), ev(2, 3000, "tool_call_update", { id: "c1", status: "failed" })];
    fake.Node.Events.mockImplementation(async (_: string, after: number) => {
      if (broken) throw new Error("the node did not answer");
      return store.filter(e => e.seq > after);
    });
    analytics.thread = "msg_a"; analytics.turn = "msg_a";
    const c = mount();
    await settle(svelte);
    const warn = document.querySelector(".rd")!;
    expect(warn.textContent).toContain("the node did not answer");
    expect([...warn.querySelectorAll("button")].map(b => b.textContent)).toContain("Retry");
    broken = false;
    (document.querySelector('header button[title="Refresh"]') as HTMLElement).click();
    await settle(svelte);
    expect(document.querySelector(".rd")).toBeNull();
    expect(document.querySelector(".empty")).toBeNull();
    expect(document.querySelector(".stats")!.textContent).toMatch(/Calls\s*1/);
    expect(document.querySelector(".stats")!.textContent).toMatch(/Errors\s*1/);
    svelte.unmount(c);
  });
});
