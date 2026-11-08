// The app's state against a mocked node: sending, following, reading a message's events.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fakeNode, deferred, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("./api", async () => {
  const errors = await import("./errors");
  // The factory runs once: the node it gives is whichever fake the test made.
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f), b64: (s: string) => s, unb64: () => new Uint8Array() };
});

async function load() {
  const { app } = await import("./state.svelte");
  app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }, { name: "codex", address: "agent:codex", driver: "acp", queued: 0, running: 0 }];
  app.conn = { ok: true };
  return app;
}

const msg = (id: string, thread: string, state = "queued", to = "agent:claude") =>
  ({ id, from: "user:me", to, thread, depth: 0, state, created_at: "", updated_at: "" });
const evs = (from: number, n: number) => Array.from({ length: n }, (_, i) => ({ seq: from + i, time: "", kind: "text", data: JSON.stringify("x") }));

beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Agents.mockResolvedValue([]);
});
afterEach(() => vi.useRealTimers());

describe("send", () => {
  it("returns false and keeps nothing sent when the node refuses (F1/Y2)", async () => {
    const app = await load();
    fake.Node.SendTask.mockRejectedValue(new Error(JSON.stringify({ code: "unavailable", message: "down", hint: "" })));
    const ok = await app.send("hello", []);
    expect(ok).toBe(false);
    expect(app.error).not.toBe("");
    expect(app.msgs).toEqual([]);
  });

  it("returns true once the node has it", async () => {
    const app = await load();
    fake.Node.SendTask.mockResolvedValue(msg("m1", "t1"));
    expect(await app.send("hello", [])).toBe(true);
    expect(app.thread).toBe("t1");
    expect(app.msgs.map(m => m.id)).toEqual(["m1"]);
  });

  it("refuses a text over the node's max_body before sending", async () => {
    const app = await load();
    app.limits = { max_body: 4, max_files: 16, max_files_size: 1 << 24 };
    expect(await app.send("hello", [])).toBe(false);
    expect(fake.Node.SendTask).not.toHaveBeenCalled();
  });

  it("puts the answer in the chat it was sent from, not the one open now (Y1)", async () => {
    const app = await load();
    const a = { key: "thread:A", kind: "thread" as const, thread: "A", title: "A", updated: "" };
    const b = { key: "thread:B", kind: "thread" as const, thread: "B", title: "B", updated: "" };
    fake.Node.Thread.mockImplementation(async (th: string) => [msg("old-" + th, th, "done")]);
    fake.Node.Events.mockResolvedValue([]);
    await app.open(a);
    const sent = deferred();
    fake.Node.SendTask.mockReturnValue(sent.promise);
    const going = app.send("to A", []);
    await app.open(b);
    sent.resolve(msg("new", "A"));
    expect(await going).toBe(true);
    expect(app.thread).toBe("B");
    expect(app.msgs.map(m => m.id)).toEqual(["old-B"]);
    expect(fake.Node.SendTask.mock.calls[0][0].thread).toBe("A");
  });

  it("does not give a new task the thread of a send made from another chat (Y1)", async () => {
    const app = await load();
    const sent = deferred();
    fake.Node.SendTask.mockReturnValue(sent.promise);
    app.target = "agent:claude";
    const going = app.send("first", []); // a new task
    app.newTask();
    sent.resolve(msg("m1", "T1"));
    await going;
    expect(app.thread).toBe("");
    expect(app.msgs).toEqual([]);
  });

  it("sends a cloud task's retry to the sandbox made the first time (F3/Y5)", async () => {
    const app = await load();
    app.draft.where = "cloud";
    app.draft.cloudAgent = "ref1";
    fake.Node.CreateCloudTask.mockResolvedValue({ agent: "agent:sb_1" });
    fake.Node.SendTask.mockRejectedValueOnce(new Error("lost")).mockResolvedValueOnce(msg("m1", "t1", "queued", "agent:sb_1"));
    expect(await app.send("do it", [])).toBe(false);
    expect(await app.send("do it", [])).toBe(true);
    expect(fake.Node.CreateCloudTask).toHaveBeenCalledTimes(1);
    expect(fake.Node.SendTask.mock.calls.map(c => c[0].to)).toEqual(["agent:sb_1", "agent:sb_1"]);
  });

  it("sends a retry of the same message with the same idempotency key, a new message with another", async () => {
    const app = await load();
    fake.Node.SendTask.mockRejectedValueOnce(new Error("lost")).mockResolvedValueOnce(msg("m1", "t1")).mockResolvedValueOnce(msg("m2", "t1"));
    await app.send("one", []);
    await app.send("one", []);
    await app.send("two", []);
    const keys = fake.Node.SendTask.mock.calls.map(c => c[0].idem);
    expect(keys[0]).toMatch(/^[0-9a-f]{32}$/);
    expect(keys[1]).toBe(keys[0]);
    expect(keys[2]).not.toBe(keys[0]);
  });

  it("sends an agent only the settings chosen for it, and only values it offers (Y11)", async () => {
    const app = await load();
    app.options = {
      "agent:claude": { agent: "agent:claude", options: [{ id: "m", name: "Model", category: "model", current: "sonnet", values: [{ value: "opus", name: "Opus" }, { value: "sonnet", name: "Sonnet" }] }] },
      "agent:codex": { agent: "agent:codex", options: [{ id: "m", name: "Model", category: "model", current: "gpt", values: [{ value: "gpt", name: "GPT" }] }] },
    };
    app.setSetting("agent:claude", "model", "opus");
    expect(app.settingsFor("agent:codex")).toEqual({});
    expect(app.settingsFor("agent:claude")).toEqual({ model: "opus" });
    app.setSetting("agent:codex", "model", "opus"); // not one of codex's
    expect(app.settingsFor("agent:codex")).toEqual({});
    // An existing Codex chat gets no Claude model.
    app.thread = "T"; app.target = "agent:codex";
    fake.Node.SendTask.mockResolvedValue(msg("m1", "T", "queued", "agent:codex"));
    await app.send("hi", []);
    expect(fake.Node.SendTask.mock.calls[0][0].model).toBeUndefined();
  });
});

describe("following a running message", () => {
  it("asks again from the last event seen when the stream breaks (F2/Y16)", async () => {
    vi.useFakeTimers();
    const app = await load();
    fake.Node.Thread.mockResolvedValue([msg("m1", "T", "running")]);
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    expect(fake.Node.Follow).toHaveBeenCalledWith("m1", 0);
    fake.emit("events:m1", { events: evs(1, 3) });
    fake.emit("events:m1", { error: "stream reset" });
    expect(fake.listeners("events:m1")).toBe(0);
    await vi.advanceTimersByTimeAsync(1000);
    expect(fake.Node.Follow).toHaveBeenLastCalledWith("m1", 3);
    expect(fake.listeners("events:m1")).toBe(1);
    // The same events again (the node sends from after=3 but may repeat): applied once.
    fake.emit("events:m1", { events: evs(3, 2) });
    expect(app.turns["m1"].last).toBe(4);
    expect(app.broken["m1"]).toBeUndefined();
  });

  it("gives up after a few tries and shows the stream as broken, no endless loop", async () => {
    vi.useFakeTimers();
    const app = await load();
    fake.Node.Thread.mockResolvedValue([msg("m1", "T", "running")]);
    fake.Node.Follow.mockRejectedValue(new Error("no stream"));
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    await vi.advanceTimersByTimeAsync(10 * 60_000);
    expect(fake.Node.Follow.mock.calls.length).toBe(6);
    expect(app.broken["m1"]).toContain("no stream");
  });

  it("does not ask again on an error that lasts (not_found)", async () => {
    vi.useFakeTimers();
    const app = await load();
    fake.Node.Thread.mockResolvedValue([msg("m1", "T", "running")]);
    fake.Node.Follow.mockRejectedValue(new Error(JSON.stringify({ code: "not_found", message: "no such message", hint: "" })));
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    await vi.advanceTimersByTimeAsync(60_000);
    expect(fake.Node.Follow).toHaveBeenCalledTimes(1);
    expect(app.broken["m1"]).toBeTruthy();
  });

  it("stops a stream started for a chat already left (Follow answered after Unfollow)", async () => {
    const app = await load();
    fake.Node.Thread.mockResolvedValue([msg("m1", "T", "running")]);
    const f = deferred();
    fake.Node.Follow.mockReturnValue(f.promise);
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    app.newTask();
    f.resolve(undefined);
    await flush();
    expect(fake.Node.Unfollow).toHaveBeenCalledTimes(2); // by reset, and once Follow answered
  });

  it("shows the node as down when the chat list says so, and follows again once it is back", async () => {
    const app = await load();
    fake.Node.Thread.mockResolvedValue([msg("m1", "T", "running")]);
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    fake.emit("events:m1", { error: "connection reset" }); // the stream died with the node
    fake.Node.Chats.mockRejectedValue(new Error(JSON.stringify({ code: "node_down", message: "not running", hint: "" })));
    await app.loadChats();
    expect(app.conn?.ok).toBe(false);
    fake.Node.Status.mockResolvedValue({ ok: true, status: {} });
    fake.Node.Chats.mockResolvedValue({ items: [] });
    fake.Node.Home.mockResolvedValue("/Users/me");
    await app.connect();
    expect(app.conn?.ok).toBe(true);
    expect(fake.listeners("events:m1")).toBe(1);
  });
});

describe("a finished message's events", () => {
  it("are read page after page until a short one (Y9)", async () => {
    const app = await load();
    fake.Node.Thread.mockResolvedValue([msg("m1", "T", "done")]);
    fake.Node.Events.mockImplementation(async (_id: string, after: number) => after === 0 ? evs(1, 500) : after === 500 ? evs(501, 120) : []);
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    await flush();
    expect(fake.Node.Events.mock.calls).toEqual([["m1", 0], ["m1", 500]]);
    expect(app.turns["m1"].last).toBe(620);
  });
});

describe("the chat list", () => {
  it("keeps the latest answer when two lists answer out of order (Y12)", async () => {
    const app = await load();
    const first = deferred(), second = deferred();
    fake.Node.Chats.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const a = app.loadChats(), b = app.loadChats();
    second.resolve({ items: [{ key: "new", kind: "thread", title: "new", updated: "" }] });
    await b;
    expect(app.loadingChats).toBe(false);
    first.resolve({ items: [{ key: "old", kind: "thread", title: "old", updated: "" }] });
    await a;
    expect(app.chats.map(c => c.key)).toEqual(["new"]);
  });

  it("stays loading while a later list is still asked", async () => {
    const app = await load();
    const first = deferred(), second = deferred();
    fake.Node.Chats.mockReturnValueOnce(first.promise).mockReturnValueOnce(second.promise);
    const a = app.loadChats(); app.loadChats();
    first.resolve({ items: [] });
    await a;
    expect(app.loadingChats).toBe(true);
    second.resolve({ items: [] });
  });
});

describe("history", () => {
  it("is not left loading by a new task opened meanwhile", async () => {
    const app = await load();
    const h = deferred();
    fake.Node.SessionHistory.mockReturnValue(h.promise);
    const opening = app.open({ key: "session:x", kind: "session", agent: "agent:claude", session: "s", title: "", updated: "" });
    expect(app.historyLoading).toBe(true);
    app.newTask();
    expect(app.historyLoading).toBe(false);
    h.resolve([]);
    await opening;
    expect(app.historyLoading).toBe(false);
  });
});
