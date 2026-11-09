// Round 2 of the review (codex-second-desk): the app's state against a mocked node.
import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { fakeNode, deferred, flush, type Fake } from "../test/fake";

let fake: Fake;
vi.mock("./api", async () => {
  const errors = await import("./errors");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f), b64: (s: string) => s, unb64: () => new Uint8Array() };
});

async function load() {
  const { app } = await import("./state.svelte");
  app.agents = [{ name: "claude", address: "agent:claude", driver: "acp", queued: 0, running: 0 }];
  app.conn = { ok: true };
  return app;
}
const msg = (id: string, thread: string, state = "queued", to = "agent:claude", extra: any = {}) =>
  ({ id, from: "user:me", to, thread, depth: 0, state, created_at: "", updated_at: "", ...extra });
const evs = (from: number, n: number) => Array.from({ length: n }, (_, i) => ({ seq: from + i, time: "", kind: "text", data: JSON.stringify("x") }));

beforeEach(() => {
  vi.resetModules();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Agents.mockResolvedValue([]);
});
afterEach(() => { vi.useRealTimers(); });

describe("what the app creates is created once (finding 1)", () => {
  it("a cloud task's sandbox is asked with one key until it is made, the answer lost or not", async () => {
    const app = await load();
    app.draft.where = "cloud"; app.draft.cloudAgent = "ref1"; app.draft.repo = "github.com/me/a";
    fake.Node.CreateCloudTask.mockRejectedValueOnce(new Error("lost")).mockResolvedValueOnce({ agent: "agent:sb_1" });
    fake.Node.SendTask.mockResolvedValue(msg("m1", "t1", "queued", "agent:sb_1"));
    expect(await app.send("do it", [])).toBe(false);
    expect(await app.send("do it", [])).toBe(true);
    const keys = fake.Node.CreateCloudTask.mock.calls.map(c => c[3]);
    expect(keys[0]).toMatch(/^[0-9a-f]{32}$/);
    expect(keys[1]).toBe(keys[0]);
  });

  it("another repository, or a new task, is another sandbox with another key", async () => {
    const app = await load();
    app.draft.where = "cloud"; app.draft.cloudAgent = "ref1"; app.draft.repo = "github.com/me/a";
    fake.Node.CreateCloudTask.mockRejectedValue(new Error("lost"));
    await app.send("do it", []);
    app.draft.repo = "github.com/me/b";
    await app.send("do it", []);
    app.newTask();
    await app.send("do it", []);
    const [a, b, c] = fake.Node.CreateCloudTask.mock.calls.map(c => c[3]);
    expect(new Set([a, b, c]).size).toBe(3);
  });

  it("a handoff asked again after a lost answer carries the first one's key; another handoff a new one", async () => {
    const app = await load();
    const req = { thread: "T", agent: "", session: "", cwd: "/w", cloud_agent: "ref1", note: "" };
    fake.Node.Handoff.mockRejectedValueOnce(new Error("lost")).mockResolvedValue({ chat: { key: "cloud:sb_1" } });
    await expect(app.handoff(req)).rejects.toThrow();
    expect((await app.handoff({ ...req })).key).toBe("cloud:sb_1");
    await app.handoff({ ...req });
    const keys = fake.Node.Handoff.mock.calls.map(c => c[0].idem);
    expect(keys[0]).toMatch(/^[0-9a-f]{32}$/);
    expect(keys[1]).toBe(keys[0]);
    expect(keys[2]).not.toBe(keys[0]); // the first one's answer came: this is a new handoff
  });

  it("a handoff with another note is another handoff", async () => {
    const app = await load();
    const req = { thread: "T", agent: "", session: "", cwd: "/w", cloud_agent: "ref1", note: "" };
    fake.Node.Handoff.mockRejectedValue(new Error("lost"));
    await app.handoff(req).catch(() => {});
    await app.handoff({ ...req, note: "and the tests" }).catch(() => {});
    const keys = fake.Node.Handoff.mock.calls.map(c => c[0].idem);
    expect(keys[1]).not.toBe(keys[0]);
  });
});

describe("one owner per subscription (finding 2)", () => {
  it("A, another chat, A again: the Unfollow of the first names the first, the new Follow another", async () => {
    const app = await load();
    fake.Node.Thread.mockImplementation(async (th: string) => th === "A" ? [msg("m1", "A", "running")] : []);
    const A = { key: "thread:A", kind: "thread" as const, thread: "A", title: "A", updated: "" };
    await app.open(A);
    await app.open({ key: "thread:B", kind: "thread", thread: "B", title: "B", updated: "" });
    await app.open(A);
    const follows = fake.Node.Follow.mock.calls.map(c => c[2]);
    const unfollows = fake.Node.Unfollow.mock.calls.filter(c => c[0] === "m1").map(c => c[1]);
    expect(follows.length).toBe(2);
    expect(follows[0]).toMatch(/^[0-9a-f]{32}$/);
    expect(follows[1]).not.toBe(follows[0]);
    expect(unfollows).toEqual([follows[0]]); // only the first: the node keeps the second, whenever it runs
  });

  it("a batch of another subscription of the same message is not this one's", async () => {
    const app = await load();
    fake.Node.Thread.mockResolvedValue([msg("m1", "T", "running")]);
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    const sub = fake.Node.Follow.mock.calls[0][2];
    fake.emit("events:m1", { id: "m1", sub: "someone-else", events: evs(1, 2) });
    expect(app.turns.m1.last).toBe(0);
    fake.emit("events:m1", { id: "m1", sub, events: evs(1, 2) });
    expect(app.turns.m1.last).toBe(2);
  });
});

describe("nothing is dropped at the end (finding 3)", () => {
  async function running() {
    const app = await load();
    fake.Node.Thread.mockResolvedValue([msg("m1", "T", "running")]);
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    const sub = fake.Node.Follow.mock.calls[0][2];
    fake.emit("events:m1", { id: "m1", sub, events: evs(1, 1) });
    return app;
  }

  it("a cancel's answer ends the stream, and the events it still held are read from the last seq", async () => {
    const app = await running();
    fake.Node.Cancel.mockResolvedValue(msg("m1", "T", "cancelled"));
    fake.Node.Events.mockResolvedValue(evs(2, 1)); // the batch still in the app's buffer
    await app.cancel("m1");
    await flush();
    expect(fake.Node.Events).toHaveBeenCalledWith("m1", 1);
    expect(app.turns.m1.last).toBe(2);
    expect(app.msgs[0].state).toBe("cancelled");
  });

  it("a read of them that fails is said on the message", async () => {
    const app = await running();
    fake.Node.Cancel.mockResolvedValue(msg("m1", "T", "cancelled"));
    fake.Node.Events.mockRejectedValue(new Error("down"));
    await app.cancel("m1");
    await flush();
    expect(app.broken.m1).toBeTruthy();
  });

  it("the stream's own final came after its last batch: nothing more is read", async () => {
    const app = await running();
    const sub = fake.Node.Follow.mock.calls[0][2];
    fake.emit("events:m1", { id: "m1", sub, final: msg("m1", "T", "done") });
    await flush();
    expect(fake.Node.Events).not.toHaveBeenCalled();
  });
});
