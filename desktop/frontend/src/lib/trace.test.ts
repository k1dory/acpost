import { describe, it, expect } from "vitest";
import { marksOf, traceTurn, phasesOf, threadSum, threadCut, uniqueTurns, decisionState, sortSums, rows, commandKey, commandGroups, clip, shown, ticks, type Mark, type TMsg, type TThread, type Span } from "./trace";

const T0 = Date.parse("2026-11-12T10:00:00Z");
const iso = (dt: number) => new Date(T0 + dt).toISOString();
const ev = (seq: number, dt: number, kind: string, data: unknown) => ({ seq, time: iso(dt), kind, data: typeof data === "string" ? data : JSON.stringify(data) });
const msg = (over: Partial<TMsg> = {}): TMsg => ({
  id: "msg_a", thread: "msg_a", depth: 0, from: "user:me", to: "agent:claude", state: "done",
  created_at: iso(0), updated_at: iso(20_000), finished_at: iso(20_000), events: [], ...over,
});
const sum = (p: Record<string, number>) => Object.values(p).reduce((a, b) => a + b, 0);

describe("marksOf", () => {
  it("keeps the timing of events, not what they carried", () => {
    const m = marksOf([
      ev(1, 1000, "thought", { text: "a" }), ev(2, 1500, "thought", { text: "b" }),
      ev(3, 2000, "tool_call", { id: "c1", kind: "execute", title: "Terminal", status: "pending" }),
      ev(4, 2100, "tool_call_update", { id: "c1", status: "completed", input: { command: ["go", "test"] }, output: "PASS", exit_code: 0 }),
      ev(5, 2200, "permission_request", { request_id: "r1", tool_call_id: "c2", title: "Write a.go" }),
      ev(6, 2300, "permission", { request_id: "r1", decision: "allow_once" }),
      ev(7, 2400, "usage", { input: 1 }),
      ev(8, 2500, "text", "not json"),
      ev(9, 2600, "tool_call", "broken"),
    ]);
    expect(m.map(x => x.kind)).toEqual(["thought", "tool_call", "tool_call_update", "permission_request", "permission", "text"]);
    expect(m[0]).toMatchObject({ at: T0 + 1000, end: T0 + 1500 });
    expect(m[2]).toMatchObject({ call: "c1", status: "completed", command: "go test", exit: 0 });
    expect(JSON.stringify(m)).not.toContain("PASS");
    expect(m[3]).toMatchObject({ request: "r1", call: "c2" });
    expect(m[4]).toMatchObject({ request: "r1", status: "allow_once" });
  });
});

describe("traceTurn", () => {
  const marks: Mark[] = [
    { seq: 1, at: T0 + 2000, end: T0 + 3000, kind: "thought" },
    { seq: 2, at: T0 + 3000, kind: "tool_call", call: "c1", tool: "execute", title: "Terminal", status: "pending" },
    { seq: 3, at: T0 + 3100, kind: "tool_call_update", call: "c1", command: "npm test" },
    { seq: 4, at: T0 + 4000, kind: "permission_request", request: "r1", call: "c1", title: "Run npm test" },
    { seq: 5, at: T0 + 7000, kind: "permission", request: "r1", status: "allow_once" },
    { seq: 6, at: T0 + 9000, kind: "tool_call_update", call: "c1", status: "completed", exit: 1 },
    { seq: 7, at: T0 + 10_000, kind: "tool_call", call: "c2", tool: "read", title: "Read a.go", status: "pending" },
    { seq: 8, at: T0 + 10_500, kind: "tool_call_update", call: "c2", status: "completed" },
    { seq: 9, at: T0 + 11_000, kind: "tool_call", call: "c3", tool: "execute", status: "pending", command: "acpost send --to agent:helper --wait" },
    { seq: 10, at: T0 + 18_000, kind: "tool_call_update", call: "c3", status: "completed" },
    { seq: 11, at: T0 + 19_000, end: T0 + 20_000, kind: "text" },
  ];
  const child = msg({ id: "msg_b", thread: "msg_b", parent: "msg_a", depth: 1, to: "agent:helper", created_at: iso(12_000), finished_at: iso(17_000), updated_at: iso(17_000) });

  it("makes spans of calls, permission waits and messages to other agents, each under what ran it", () => {
    const tr = traceTurn(msg(), marks, [child, msg({ id: "msg_x", parent: "msg_other" })], T0 + 99_000);
    expect(tr.running).toBe(false);
    expect(tr.start).toBe(T0);
    expect(tr.first).toBe(T0 + 2000);
    expect(tr.end).toBe(T0 + 20_000);
    const by = Object.fromEntries(tr.spans.map(s => [s.id, s]));
    expect(Object.keys(by).sort()).toEqual(["msg:msg_b", "perm:r1", "tool:c1", "tool:c2", "tool:c3"]);
    expect(by["tool:c1"]).toMatchObject({ name: "execute", label: "npm test", start: T0 + 3000, end: T0 + 9000, state: "failed", exit: 1 });
    expect(by["tool:c2"]).toMatchObject({ name: "read", label: "Read a.go", state: "done" });
    expect(by["perm:r1"]).toMatchObject({ parent: "tool:c1", start: T0 + 4000, end: T0 + 7000, state: "done" });
    expect(by["msg:msg_b"]).toMatchObject({ parent: "tool:c3", name: "helper", start: T0 + 12_000, end: T0 + 17_000, state: "done" });
    expect(tr.calls).toBe(3);
    expect(tr.errors).toBe(1);
    expect(tr.active).toBe(18_000);
  });

  it("splits the turn into phases that sum to its length", () => {
    const tr = traceTurn(msg(), marks, [child], T0 + 99_000);
    expect(tr.phases).toEqual({
      queue: 2000,                       // sent → first event
      approval: 3000,                    // 4 → 7 s, inside c1
      agents: 5000,                      // 12 → 17 s, inside c3
      tools: (9000 - 3000 - 3000) + 500 + (7000 - 5000), // c1 without the wait, c2, c3 without the agent
      model: 1000 + 1000 + 500 + 2000,   // 2→3, 9→10, 10.5→11, 18→20 s
    } as any);
    expect(sum(tr.phases)).toBe(20_000);
  });

  it("a running turn ends now, and so do its unfinished calls", () => {
    const tr = traceTurn(msg({ state: "running", finished_at: undefined }), marks.slice(0, 4), [], T0 + 30_000);
    expect(tr.running).toBe(true);
    expect(tr.end).toBe(T0 + 30_000);
    const c1 = tr.spans.find(s => s.id === "tool:c1")!;
    expect(c1).toMatchObject({ end: T0 + 30_000, state: "running" });
    expect(tr.spans.find(s => s.id === "perm:r1")!.state).toBe("waiting");
    expect(sum(tr.phases)).toBe(30_000);
  });

  it("a turn that never began waited all of its time", () => {
    const tr = traceTurn(msg({ state: "expired", finished_at: iso(5000) }), [], [], T0 + 99_000);
    expect(tr.phases.queue).toBe(5000);
    expect(tr.active).toBe(0);
    expect(tr.errors).toBe(1);
  });

  it("what a cancelled turn left unfinished is cancelled; a permission refused failed", () => {
    const tr = traceTurn(msg({ state: "cancelled" }), [
      { seq: 1, at: T0 + 1000, kind: "tool_call", call: "c", status: "pending" },
      { seq: 2, at: T0 + 2000, kind: "permission_request", request: "r", call: "nope" },
      { seq: 3, at: T0 + 3000, kind: "permission", request: "r", status: "reject_once" },
    ], [], T0);
    expect(tr.spans.find(s => s.kind === "tool")!.state).toBe("cancelled");
    const p = tr.spans.find(s => s.kind === "permission")!;
    expect(p.state).toBe("failed");
    expect(p.parent).toBeUndefined(); // its call was not seen
  });
});

describe("phasesOf", () => {
  it("a moment is waiting for a person before another agent before a tool", () => {
    const s = (kind: Span["kind"], a: number, b: number): Span => ({ id: kind + a, kind, ref: "", name: "", label: "", start: a, end: b, state: "done" });
    const p = phasesOf(0, 0, 100, [s("tool", 0, 100), s("agent", 10, 60), s("permission", 50, 70)]);
    expect(p).toEqual({ queue: 0, model: 0, tools: 40, agents: 40, approval: 20 });
  });
});

describe("threads", () => {
  const th = (id: string, updated: number, turns: TMsg[]): TThread => ({ thread: id, title: id, updated_at: iso(updated), messages: turns.map(m => (m.parent ? m : { ...m, thread: id })) });
  it("sums a conversation's own turns; the turns it asked are their time, not more turns", () => {
    const a = msg({ events: [{ seq: 1, at: T0 + 1000, kind: "tool_call", call: "c", status: "failed" }] });
    const b = msg({ id: "msg_b", parent: "msg_a", thread: "msg_b", depth: 1, created_at: iso(1000), finished_at: iso(5000), events: [{ seq: 1, at: T0 + 1000, kind: "tool_call", call: "x" }] });
    const s = threadSum(th("msg_a", 1, [a, b]), T0);
    expect(s).toMatchObject({ turns: 1, calls: 1, errors: 1, active: 19_000, running: false });
    expect(s.phases.agents).toBe(4000);
  });
  it("sorts by the latest or by the most time", () => {
    const list = [
      { ...threadSum(th("old-long", 1, [msg({ finished_at: iso(90_000), events: [{ seq: 1, at: T0, kind: "text" }] })]), T0) },
      { ...threadSum(th("new-short", 2, [msg({ events: [{ seq: 1, at: T0, kind: "text" }] })]), T0) },
    ];
    expect(sortSums(list, "recent").map(x => x.thread)).toEqual(["new-short", "old-long"]);
    expect(sortSums(list, "time").map(x => x.thread)).toEqual(["old-long", "new-short"]);
  });
});

describe("rows", () => {
  const sp = (id: string, start: number, end: number, parent?: string, name = "execute", label = id): Span => ({ id, kind: "tool", ref: id, name, label, start, end, state: "done", parent });
  const spans = [sp("a", 0, 5000), sp("b", 100, 300, "a", "agent", "helper"), sp("c", 6000, 6200, undefined, "read", "README.md"), sp("d", 7000, 9000)];
  it("puts each span under what ran it", () => {
    expect(rows(spans).map(r => `${r.id}${r.depth}`)).toEqual(["a0", "b1", "c0", "d0"]);
  });
  it("filters by name or label, keeping what a kept span ran under", () => {
    expect(rows(spans, { text: "HELPER" }).map(r => r.id)).toEqual(["a", "b"]);
    expect(rows(spans, { text: "readme" }).map(r => r.id)).toEqual(["c"]);
  });
  it("hides what took under a second", () => {
    expect(rows(spans, { hideShort: true }).map(r => r.id)).toEqual(["a", "d"]);
  });
  it("a span that names itself as its parent is not lost", () => {
    expect(rows([sp("x", 0, 1, "x")]).map(r => r.id)).toEqual(["x"]);
  });
});

describe("commands", () => {
  it("names a command by its program, and a subcommand where the program has them", () => {
    const k = (command?: string, name = "execute") => commandKey({ command, name });
    expect(k("git status --short")).toBe("git status");
    expect(k("cd /x && go test ./...")).toBe("go test");
    expect(k("FOO=1 sudo /usr/bin/make build")).toBe("make build");
    expect(k("ls -la | head")).toBe("ls");
    expect(k("npm --version")).toBe("npm");
    expect(k("/bin/zsh -lc 'echo hi'")).toBe("zsh");
    expect(k(undefined, "read")).toBe("read");
    expect(k("   ")).toBe("execute");
  });
  it("groups calls: runs, total, average, slowest, failures", () => {
    const s = (command: string | undefined, d: number, state: Span["state"] = "done", name = "execute"): Span => ({ id: Math.random() + "", kind: "tool", ref: "", name, label: "", start: 0, end: d, state, command });
    const g = commandGroups([s("go test ./a", 3000), s("go test ./b", 1000, "failed"), s(undefined, 500, "done", "read"),
      { ...s("x", 9999), kind: "agent" }]);
    expect(g).toEqual([
      { key: "go test", runs: 2, total: 4000, avg: 2000, max: 3000, failures: 1 },
      { key: "read", runs: 1, total: 500, avg: 500, max: 500, failures: 0 },
    ]);
  });
});

describe("helpers", () => {
  it("bounds what the detail pane shows", () => {
    expect(clip("abcdef", 3)).toBe("abc\n… (3 more)");
    expect(shown({ a: 1 })).toBe('{\n  "a": 1\n}');
    expect(shown(undefined)).toBe("");
    expect(shown("x".repeat(20), 10)).toContain("(10 more)");
  });
  it("puts ticks at round steps", () => {
    expect(ticks(10_000)).toEqual([0, 2000, 4000, 6000, 8000, 10_000]);
    expect(ticks(0)).toEqual([0]);
    expect(ticks(500).length).toBeLessThanOrEqual(7);
  });
});

describe("review round 1", () => {
  it("a call ends when it first says so: the full events and the node's short form give the same timings", () => {
    const full = marksOf([
      ev(1, 1000, "tool_call", { id: "c", kind: "execute", status: "pending" }),
      ev(2, 2000, "tool_call_update", { id: "c", status: "completed" }),
      ev(3, 9000, "tool_call_update", { id: "c", status: "completed", output: "late output", exit_code: 0 }),
    ]);
    // What GET /v1/traces gives for the same events: a repeated status is left out, the exit code kept.
    const compact: Mark[] = [
      { seq: 1, at: T0 + 1000, kind: "tool_call", call: "c", tool: "execute", status: "pending" },
      { seq: 2, at: T0 + 2000, kind: "tool_call_update", call: "c", status: "completed" },
      { seq: 3, at: T0 + 9000, kind: "tool_call_update", call: "c", exit: 0 },
    ];
    const a = traceTurn(msg(), full, [], T0), b = traceTurn(msg(), compact, [], T0);
    expect(a.spans.map(s => [s.start, s.end, s.state, s.exit])).toEqual([[T0 + 1000, T0 + 2000, "done", 0]]);
    expect(b.spans.map(s => [s.start, s.end, s.state, s.exit])).toEqual(a.spans.map(s => [s.start, s.end, s.state, s.exit]));
    expect(b.phases).toEqual(a.phases);
    // A late non-zero exit code still fails it, without moving its end.
    const late = traceTurn(msg(), [...compact.slice(0, 2), { seq: 3, at: T0 + 9000, kind: "tool_call_update", call: "c", exit: 2 }], [], T0);
    expect(late.spans[0]).toMatchObject({ end: T0 + 2000, state: "failed" });
  });

  it("a turn whose events the node stopped keeping says so, and so does its conversation", () => {
    const marks = marksOf([ev(1, 1000, "tool_call", { id: "c", status: "pending" }), ev(2, 2000, "truncated", "later events were not kept")]);
    expect(traceTurn(msg(), marks, [], T0).truncated).toBe(true);
    expect(traceTurn(msg(), marks.slice(0, 1), [], T0).truncated).toBe(false);
    const th: TThread = { thread: "msg_a", title: "", updated_at: iso(0), messages: [msg({ events: marks })] };
    expect(threadSum(th, T0).cut).toBe(true);
    expect(threadSum({ ...th, messages: [msg({ events: marks.slice(0, 1) })] }, T0).cut).toBe(false);
  });
});

describe("review round 2", () => {
  it("a conversation is cut when any turn it holds was, its own or one it asked", () => {
    const own = msg();
    const helper = msg({ id: "msg_h", thread: "msg_h", parent: "msg_a", depth: 1, events: [{ seq: 1, at: T0, kind: "truncated" }] });
    const th: TThread = { thread: "msg_a", title: "", updated_at: iso(0), messages: [own, helper] };
    expect(threadCut(th)).toBe(true);
    expect(threadSum(th, T0).cut).toBe(true);
    expect(threadCut({ ...th, messages: [own, { ...helper, events: [] }] })).toBe(false);
    expect(threadCut({ ...th, messages: [own, { ...helper, events: [], cut: true }] })).toBe(true);
    expect(threadCut({ ...th, messages: [own], cut: true })).toBe(true);
  });
});

describe("review round 3", () => {
  it("an event after a finished turn's end extends neither it, its spans nor its phases", () => {
    const m = msg({ finished_at: iso(2000), updated_at: iso(10_000) });
    const marks: Mark[] = [
      { seq: 1, at: T0 + 500, kind: "tool_call", call: "c", status: "pending" },
      { seq: 2, at: T0 + 10_000, kind: "notice", status: "warning" }, // a cancel that raced the reply
    ];
    const tr = traceTurn(m, marks, [], T0 + 99_000);
    expect(tr.end).toBe(T0 + 2000);
    expect(tr.active).toBe(1500);
    expect(sum(tr.phases)).toBe(2000);
    expect(tr.spans[0]).toMatchObject({ start: T0 + 500, end: T0 + 2000, state: "done" });
    // A call recorded after the end is shown at the end, with no length.
    const late = traceTurn(m, [...marks, { seq: 3, at: T0 + 9000, kind: "tool_call", call: "z" }], [], T0);
    expect(late.spans.find(s => s.ref === "z")).toMatchObject({ start: T0 + 2000, end: T0 + 2000 });
    expect(late.end).toBe(T0 + 2000);
  });

  it("a turn two conversations hold is counted once, the copy not cut kept", () => {
    const callMarks: Mark[] = [{ seq: 1, at: T0 + 1000, kind: "tool_call", call: "c", tool: "read" }, { seq: 2, at: T0 + 2000, kind: "tool_call_update", call: "c", status: "completed" }];
    const sub = msg({ id: "msg_s", thread: "msg_s", parent: "msg_a", depth: 1, events: callMarks });
    const parent: TThread = { thread: "msg_a", title: "", updated_at: iso(0), messages: [msg(), { ...sub, cut: true, events: [] }] };
    const continued: TThread = { thread: "msg_s", title: "", updated_at: iso(0), messages: [sub] };
    const turns = uniqueTurns([parent, continued]);
    expect(turns.map(x => x.msg.id)).toEqual(["msg_a", "msg_s"]);
    expect(turns[1].msg.cut).toBeFalsy();
    expect(turns[1].msg.events.length).toBe(2);
  });
});

describe("review round 4", () => {
  it("a turn that starts with a plan began then, through both forms alike", () => {
    const m = msg({ finished_at: iso(10_000) });
    const full = marksOf([
      ev(1, 1000, "plan", ["pending: a step"]),
      ev(2, 5000, "usage", { input: 1 }),
      ev(3, 9000, "text", { text: "done" }),
    ]);
    // What GET /v1/traces gives for them: the first event's time, whatever it was, and the text run.
    const compact: Mark[] = [{ seq: 1, at: T0 + 1000, kind: "plan" }, { seq: 3, at: T0 + 9000, end: T0 + 9000, kind: "text" }];
    expect(full).toEqual(compact);
    for (const marks of [full, compact]) {
      const tr = traceTurn(m, marks, [], T0);
      expect(tr.first).toBe(T0 + 1000);
      expect(tr.phases.queue).toBe(1000);
      expect(tr.active).toBe(9000);
    }
  });

  it("a permission's decision is read by its words: a sandbox's \"selected\" is answered, not failed", () => {
    expect(["allow_once", "allow_always", "allowed"].map(decisionState)).toEqual(["done", "done", "done"]);
    expect(["reject_once", "reject_always", "rejected", "timeout"].map(decisionState)).toEqual(["failed", "failed", "failed", "failed"]);
    expect(["cancelled", "cancelled (no matching option)", "undelivered", "superseded"].map(decisionState)).toEqual(["cancelled", "cancelled", "cancelled", "cancelled"]);
    expect(["selected", "something new", "", undefined].map(decisionState)).toEqual(["answered", "answered", "answered", "answered"]);
    // The sandbox's events (internal/node sandbox.go: the request, then "selected" by sandbox:user).
    const sandbox = marksOf([
      ev(1, 1000, "permission_request", { request_id: "req_00000000000000aa", kind: "permission", title: "Run rm -rf build" }),
      ev(2, 3000, "permission", { request_id: "req_00000000000000aa", decision: "selected", by: "sandbox:user" }),
    ]);
    const p = traceTurn(msg(), sandbox, [], T0).spans.find(s => s.kind === "permission")!;
    expect(p).toMatchObject({ state: "answered", start: T0 + 1000, end: T0 + 3000 });
    // ACP's (internal/node ask.go: the option's kind).
    const acp = marksOf([
      ev(1, 1000, "permission_request", { request_id: "r", tool_call_id: "c", title: "Write a.go" }),
      ev(2, 2000, "permission", { request_id: "r", decision: "allow_once", by: "user:me" }),
    ]);
    expect(traceTurn(msg(), acp, [], T0).spans.find(s => s.kind === "permission")!.state).toBe("done");
  });
});
