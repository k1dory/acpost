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
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });

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

describe("send, review round 1", () => {
  it("a new task does not reuse the previous attempt's sandbox or key (R1.2)", async () => {
    const app = await load();
    app.draft.where = "cloud"; app.draft.cloudAgent = "ref1";
    fake.Node.CreateCloudTask.mockResolvedValueOnce({ agent: "agent:sb_1" }).mockResolvedValueOnce({ agent: "agent:sb_2" });
    fake.Node.SendTask.mockRejectedValueOnce(new Error("lost")).mockResolvedValueOnce(msg("m2", "t2", "queued", "agent:sb_2"));
    expect(await app.send("do it", [])).toBe(false);
    app.newTask();
    expect(await app.send("do it", [])).toBe(true);
    expect(fake.Node.CreateCloudTask).toHaveBeenCalledTimes(2);
    const [a, b] = fake.Node.SendTask.mock.calls.map(c => c[0]);
    expect(b.to).toBe("agent:sb_2");
    expect(b.idem).not.toBe(a.idem);
  });

  it("a sandbox made after a new task was started is not given to it (R1.2)", async () => {
    const app = await load();
    app.draft.where = "cloud"; app.draft.cloudAgent = "ref1";
    const made = deferred();
    fake.Node.CreateCloudTask.mockReturnValueOnce(made.promise).mockResolvedValueOnce({ agent: "agent:sb_2" });
    fake.Node.SendTask.mockImplementation(async (t: any) => msg("m-" + t.to, "t-" + t.to, "queued", t.to));
    const first = app.send("do it", []);          // the first task, still making its sandbox
    app.newTask();
    made.resolve({ agent: "agent:sb_1" });
    expect(await first).toBe(true);               // the first goes on, to its own sandbox
    await app.send("do it", []);                  // the new task: a sandbox of its own
    expect(fake.Node.SendTask.mock.calls.map(c => c[0].to)).toEqual(["agent:sb_1", "agent:sb_2"]);
  });

  it("a different file of the same length is another message, with another key (R1.3)", async () => {
    const app = await load();
    fake.Node.SendTask.mockRejectedValue(new Error("lost"));
    await app.send("see", [{ name: "f", type: "text/plain", data: "YQ==" }]);
    await app.send("see", [{ name: "f", type: "text/plain", data: "Yg==" }]);
    await app.send("see", [{ name: "f", type: "text/csv", data: "Yg==" }]);
    const keys = fake.Node.SendTask.mock.calls.map(c => c[0].idem);
    expect(new Set(keys).size).toBe(3);
  });

  it("a retry whose message the chat already shows does not show it twice (R1.4)", async () => {
    const app = await load();
    const chat = { key: "thread:T", kind: "thread" as const, thread: "T", agent: "agent:claude", title: "", updated: "" };
    fake.Node.Thread.mockResolvedValueOnce([]).mockResolvedValueOnce([msg("m1", "T", "running")]);
    await app.open(chat);
    fake.Node.SendTask.mockRejectedValueOnce(new Error("timeout")).mockResolvedValueOnce(msg("m1", "T", "running"));
    expect(await app.send("hi", [])).toBe(false); // the node took it; its answer was lost
    await app.open(chat);                          // reopened: the thread has m1
    expect(await app.send("hi", [])).toBe(true);   // same key: the node gives m1 back
    expect(fake.Node.SendTask.mock.calls[1][0].idem).toBe(fake.Node.SendTask.mock.calls[0][0].idem);
    expect(app.msgs.map(m => m.id)).toEqual(["m1"]);
  });

  it("a draft outlives an outage of the node (R1.1)", async () => {
    const app = await load();
    app.thread = "T"; app.target = "agent:claude";
    const d = app.draftOf();
    d.text = "keep me";
    fake.Node.SendTask.mockRejectedValue(new Error(JSON.stringify({ code: "node_down", message: "down", hint: "" })));
    expect(await app.submit(d)).toBe(false);
    fake.Node.Chats.mockRejectedValue(new Error(JSON.stringify({ code: "node_down", message: "down", hint: "" })));
    await app.loadChats();
    expect(app.conn?.ok).toBe(false);
    fake.Node.Status.mockResolvedValue({ ok: true, status: {} });
    fake.Node.Chats.mockResolvedValue({ items: [] });
    await app.connect();
    expect(app.draftOf().text).toBe("keep me");
  });
});

describe("send, review round 2", () => {
  const file = (name: string) => ({ name, type: "text/plain", size: 1, data: "YQ==" });

  it("a late answer does not edit the new task started meanwhile (R2.1)", async () => {
    const app = await load();
    const d = app.draftOf();
    d.text = "original task"; d.files = [file("a.txt")];
    const sent = deferred();
    fake.Node.SendTask.mockReturnValue(sent.promise);
    const going = app.submit(d);
    app.newTask();
    const n = app.draftOf();
    expect(n).not.toBe(d);
    expect(n.text).toBe(""); // what is being sent is the first task's
    n.text = "new requirement"; n.files = [file("b.txt")];
    sent.resolve(msg("m1", "T1"));
    expect(await going).toBe(true);
    expect(app.draftOf().text).toBe("new requirement");
    expect(app.draftOf().files.map(f => f.name)).toEqual(["b.txt"]);
  });

  it("a new task with nothing being sent keeps what was typed (R2.1)", async () => {
    const app = await load();
    app.draftOf().text = "half typed";
    app.newTask();
    expect(app.draftOf().text).toBe("half typed");
  });

  it("a send that fails after a new task was started gives its text back (R2.1)", async () => {
    const app = await load();
    const d = app.draftOf();
    d.text = "first";
    const sent = deferred();
    fake.Node.SendTask.mockReturnValue(sent.promise);
    const going = app.submit(d);
    app.newTask();
    sent.reject(new Error("down"));
    expect(await going).toBe(false);
    expect(app.draftOf().text).toBe("first");
  });

  it("the answer reaches its chat left and opened again, A to B to A (R2.2)", async () => {
    const app = await load();
    const A = { key: "thread:A", kind: "thread" as const, thread: "A", agent: "agent:claude", title: "", updated: "" };
    const B = { key: "thread:B", kind: "thread" as const, thread: "B", agent: "agent:claude", title: "", updated: "" };
    fake.Node.Thread.mockResolvedValue([]); // the node has not taken it yet when A loads again
    await app.open(A);
    app.draftOf().text = "to A";
    const sent = deferred();
    fake.Node.SendTask.mockReturnValue(sent.promise);
    const going = app.submit(app.draftOf());
    await app.open(B);
    await app.open(A);
    sent.resolve(msg("m1", "A", "queued"));
    expect(await going).toBe(true);
    expect(app.msgs.map(m => m.id)).toEqual(["m1"]);
    expect(fake.Node.Follow).toHaveBeenCalledWith("m1", 0);
    expect(app.draftOf().text).toBe("");
  });

  it("a session continued here: its draft is the thread's it becomes (R2.3)", async () => {
    const app = await load();
    const S = { key: "session:claude/s1", kind: "session" as const, agent: "agent:claude", session: "s1", title: "", updated: "" };
    fake.Node.SessionHistory.mockResolvedValue([]);
    await app.open(S);
    const d = app.draftOf();
    expect(d.key).toBe("session:claude/s1");
    d.text = "go on";
    const sent = deferred();
    fake.Node.SendTask.mockReturnValue(sent.promise);
    const going = app.submit(d);
    d.text = "go on\nthen this";
    sent.resolve(msg("m1", "T1"));
    await going;
    expect(app.draftKey()).toBe("thread:T1");
    expect(app.draftOf().text).toBe("then this");
    // The session leaves the list; the thread opened later has the draft.
    fake.Node.Thread.mockResolvedValue([msg("m1", "T1", "done")]);
    await app.open({ key: "thread:T1", kind: "thread", thread: "T1", agent: "agent:claude", title: "", updated: "" });
    expect(app.draftOf().text).toBe("then this");
  });

  it("a new task left for another chat before its answer: the rest goes to its thread (R2.3)", async () => {
    const app = await load();
    const d = app.draftOf();
    d.text = "task";
    const sent = deferred();
    fake.Node.SendTask.mockReturnValue(sent.promise);
    const going = app.submit(d);
    d.text = "task\nmore";
    fake.Node.Thread.mockResolvedValue([]);
    await app.open({ key: "thread:B", kind: "thread", thread: "B", agent: "agent:claude", title: "", updated: "" });
    sent.resolve(msg("m1", "T1"));
    await going;
    expect(app.thread).toBe("B");
    expect(app.draftOf("thread:T1").text).toBe("more");
    expect(app.draftOf("new").text).toBe("");
  });

  it("a thread's own draft is kept when another moves into it (R2.3)", async () => {
    const app = await load();
    app.draftOf("thread:T1").text = "already here";
    const d = app.draftOf();
    d.text = "task";
    const sent = deferred();
    fake.Node.SendTask.mockReturnValue(sent.promise);
    const going = app.submit(d);
    d.text = "task\nleft";
    sent.resolve(msg("m1", "T1"));
    await going;
    expect(app.draftOf("thread:T1").text).toBe("already here\nleft");
  });
});

describe("attaching files (R1.6)", () => {
  it("two drops at once do not both pass max_files", async () => {
    const { ComposerDraft } = await import("./composer.svelte");
    const d = new ComposerDraft("new");
    const slow = (name: string) => ({ name, type: "text/plain", size: 1, arrayBuffer: () => new Promise<ArrayBuffer>(r => setTimeout(() => r(new Uint8Array([97]).buffer), 5)) });
    const fails: string[] = [];
    const lim = { max_body: 1 << 20, max_files: 1, max_files_size: 1 << 20 };
    await Promise.all([d.add([slow("a")], lim, w => fails.push(w)), d.add([slow("b")], lim, w => fails.push(w))]);
    expect(d.files.map(f => f.name)).toEqual(["a"]);
    expect(fails).toEqual(["count"]);
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

describe("opening a thread (F5)", () => {
  it("of fifty messages asks the thread once and each message's events once, no trees", async () => {
    const app = await load();
    fake.Node.Thread.mockResolvedValue(Array.from({ length: 50 }, (_, i) => msg("m" + i, "T", "done")));
    fake.Node.Events.mockResolvedValue(evs(1, 3));
    await app.open({ key: "thread:T", kind: "thread", thread: "T", title: "", updated: "" });
    await flush();
    expect(fake.Node.Thread).toHaveBeenCalledTimes(1);
    expect(fake.Node.Events).toHaveBeenCalledTimes(50);
    expect(fake.Node.Tree).not.toHaveBeenCalled();
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

describe("what the app remembers does not grow without end (P3)", () => {
  it("forgets approvals answered", async () => {
    const app = await load();
    vi.stubGlobal("document", { hasFocus: () => true });
    fake.Node.Asking.mockResolvedValueOnce([{ request_id: "r1" }, { request_id: "r2" }]).mockResolvedValueOnce([{ request_id: "r2" }]);
    await app.watchAsking();
    await app.watchAsking();
    expect([...(app as any).asked]).toEqual(["r2"]);
  });
  it("forgets chats no longer listed, and when a finished message started", async () => {
    const app = await load();
    fake.Node.Chats.mockResolvedValueOnce({ items: [{ key: "a", kind: "thread", running: 1, title: "", updated: "" }] }).mockResolvedValueOnce({ items: [] });
    await app.loadChats();
    await app.loadChats();
    expect((app as any).wasRunning.size).toBe(0);
    app.seenRunning("m1");
    app.replace(msg("m1", "T", "done"));
    expect(app.runningSince.has("m1")).toBe(false);
  });
});

describe("the preview (P3)", () => {
  it("says a reload that fails instead of leaving it unhandled", async () => {
    const app = await load();
    fake.Node.PreviewReload.mockRejectedValue(new Error("no page"));
    fake.Node.PreviewBack.mockRejectedValue(new Error("no history"));
    app.previewReload(true);
    await flush();
    expect(app.error).toContain("no page");
    app.previewBack();
    await flush();
    expect(app.error).toContain("no history");
  });
  it("clears its list as cleared from the page shown", async () => {
    const app = await load();
    app.preview.url = "http://localhost:5173/";
    app.preview.events = [{ kind: "console.error", message: "x" }];
    app.clearPreview();
    expect(app.preview).toMatchObject({ events: [], cleared: true, clearedUrl: "http://localhost:5173/" });
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
