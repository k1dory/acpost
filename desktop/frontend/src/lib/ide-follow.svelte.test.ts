// Beside the IDE, a file the agent edits is shown there: what it edits while its chat is open.
import { describe, it, expect, vi, beforeEach } from "vitest";
import { fakeNode, flush, type Fake } from "../test/fake";
import { Turn } from "./turn";

let fake: Fake;
vi.mock("./api", async () => {
  const errors = await import("./errors");
  const Node = new Proxy({}, { get: (_, k: string) => fake.Node[k] });
  return { ...errors, Node, on: (n: string, f: (d: any) => void) => fake.on(n, f), b64: (s: string) => s, unb64: () => new Uint8Array() };
});

beforeEach(() => {
  vi.resetModules();
  vi.useRealTimers();
  fake = fakeNode();
  fake.Node.Chats.mockResolvedValue({ items: [] });
  fake.Node.Agents.mockResolvedValue([]);
});

const done = (seq: number, id: string, path: string, at: number) => ({ seq, kind: "tool_call", time: new Date(at).toISOString(),
  data: JSON.stringify({ id, status: "completed", edits: [{ path, added: 1, removed: 1, old: "a", new: "b" }] }) });

describe("the IDE follows the agent", () => {
  it("shows an edit made while the chat is open; not one made while it was left, on coming back (review round 2)", async () => {
    const { app } = await import("./state.svelte");
    const a = app as any;
    const msg = { id: "m1", from: "user:me", to: "agent:claude", thread: "t", depth: 0, state: "running", created_at: "", updated_at: "", meta: { acp_cwd: "/w" } };
    const open = () => { a.msgs = [msg]; a.follow("m1", new Turn(), 0); };
    app.view = "ide";
    open();
    await new Promise(r => setTimeout(r, 5));
    fake.emit("events:m1", { events: [done(1, "c1", "/w/a.txt", Date.now())] });
    expect(fake.Node.IDEReveal.mock.calls.map(c => c[1])).toEqual(["/w/a.txt"]);
    expect(fake.Node.IDEReveal.mock.calls[0][0]).toBe("/w");

    // Another chat; meanwhile the agent edits; back within seconds: its events are replayed.
    a.reset();
    const away = Date.now();
    await new Promise(r => setTimeout(r, 5));
    open();
    fake.emit("events:m1", { events: [done(1, "c1", "/w/a.txt", away - 50), done(2, "c2", "/w/b.txt", away)] });
    expect(fake.Node.IDEReveal).toHaveBeenCalledTimes(1);
    await new Promise(r => setTimeout(r, 5));
    fake.emit("events:m1", { events: [done(3, "c3", "/w/c.txt", Date.now())] });
    expect(fake.Node.IDEReveal.mock.calls.map(c => c[1])).toEqual(["/w/a.txt", "/w/c.txt"]);

    // An agent elsewhere (a cloud sandbox, another node): its paths are not this Mac's.
    for (const [mid, to] of [["m2", "agent:sb_123"], ["m3", "agent:claude@other"]]) {
      a.msgs = [msg, { ...msg, id: mid, to }];
      a.follow(mid, new Turn(), 0);
      await new Promise(r => setTimeout(r, 5));
      fake.emit("events:" + mid, { events: [done(1, "r1", "/w/a.txt", Date.now())] });
    }
    expect(fake.Node.IDEReveal).toHaveBeenCalledTimes(2);

    // The follow turned off, or another view: nothing is asked of the IDE.
    app.setIdeFollow(false);
    fake.emit("events:m1", { events: [done(4, "c4", "/w/d.txt", Date.now())] });
    app.setIdeFollow(true); app.view = "chat";
    fake.emit("events:m1", { events: [done(5, "c5", "/w/e.txt", Date.now())] });
    await flush();
    expect(fake.Node.IDEReveal).toHaveBeenCalledTimes(2);
  });
});
